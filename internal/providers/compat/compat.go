// Package compat turns provider/model compatibility into data.
//
// Compatibility used to be sniffed from strings at request time: a provider whose
// *name* contained "ollama" silently changed request semantics, and every new
// quirk became another `if` in the request builder. Here the traits are declared,
// resolved once per provider/model, and read back as a pre-built object.
//
// # Layer order (fixed; later layers override earlier ones)
//
//  1. endpoint family — what the endpoint *is* (openai-native, ollama, together,
//     fireworks, dashscope, openrouter, …). Taken from the declared
//     endpoint_family when present, otherwise inferred from the wire API, the
//     provider_type and the base URL. NEVER from the provider name: naming a
//     provider "ollama-proxy" must not change its request semantics (the bug this
//     package exists to fix). The bundled seeds (quirks.go) are this layer.
//  2. gateway/auth overlay — declared gateway shape (extra headers, store
//     support, identity headers) plus operator quirk rows, which may also
//     suppress a bundled rule by declaring the same (wire, family, pattern) with
//     enabled=false.
//  3. model metadata/compat — llm_models.compat (max-tokens field, strict-tools
//     opt-out, tool dialect, extra body). Applied last so a model can override
//     its provider.
//  4. request context — applied by the caller at request time (thinking on/off),
//     never by Resolve. The caller selects `Resolved.WhenThinking` for a
//     thinking request, which keeps the request path to a pointer swap.
//
// Resolve runs once per provider and once per catalogued model when the provider
// is built; the request path only reads the result (no per-request map spreading
// or allocation). ResolveCount instruments that invariant.
package compat

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// Settings are the provider-level resolver inputs. They are plain values on
// purpose: internal/store imports internal/providers, so this package must never
// import internal/store.
type Settings struct {
	// ProviderName is diagnostic only. It is deliberately NOT a matching input.
	ProviderName string
	// ProviderType is the declared llm_providers.provider_type.
	ProviderType string
	// APIBase is the provider's effective base URL.
	APIBase string
	// EndpointFamily is the declared family (llm_providers settings / brand data).
	// Empty means "infer".
	EndpointFamily string
	// Quirks are operator-declared rows for this wire API. Bundled seeds are
	// always applied; these override or suppress them.
	Quirks []Quirk
}

// ModelInfo is the model-scoped resolver input.
type ModelInfo struct {
	// ID is the vendor-native model id. Used for pattern matching and for the
	// max-tokens field rule (gpt-5/o-series use max_completion_tokens).
	ID string
	// Compat is the llm_models.compat JSONB fragment (may be empty).
	Compat json.RawMessage
	// Family is a model-declared endpoint family override (rare).
	Family string
}

// Resolved is the pre-built compat object. Every field is a request fragment the
// transports apply directly; nothing here is recomputed per request.
type Resolved struct {
	// Family is the resolved endpoint family ("" when unknown).
	Family string

	// --- endpoint family behaviour (layer 1) ---

	// NativeChatPath replaces the provider's configured chat path when non-empty
	// (Ollama's native /api/chat, which honours options.num_ctx unlike the
	// OpenAI-compat shim).
	NativeChatPath string
	// OllamaOptions injects body["options"] = {"num_ctx": ...}.
	OllamaOptions bool
	// OllamaThink injects body["think"] from Think.
	OllamaThink bool
	// StreamOptions sends stream_options.include_usage when streaming. Together
	// rejects it with HTTP 400.
	StreamOptions bool
	// ClampMaxTokens (>0) caps max_tokens for non-streaming requests. Fireworks
	// requires stream=true above 4096.
	ClampMaxTokens int
	// SystemCacheControl wraps the first system message with cache_control blocks.
	SystemCacheControl bool
	// ToolPrefixCache puts cache_control on the last tool definition.
	ToolPrefixCache bool
	// DashScopePassthrough allows the enable_thinking / thinking_budget keys.
	DashScopePassthrough bool
	// SupportsThinking is false for endpoints that must suppress reasoning
	// (Ollama models such as qwq/deepseek-r1 enable it by default).
	SupportsThinking bool

	// --- gateway/auth overlay (layer 2) ---

	// SupportsDeveloperRole maps the "system" role to "developer" (native OpenAI
	// only; proxies and third-party backends reject the role).
	SupportsDeveloperRole bool
	// SupportsStore allows forwarding a caller-supplied "store" flag.
	SupportsStore bool
	// Headers are merged into every outgoing request.
	Headers map[string]string
	// ExtraBody is merged into every request body.
	ExtraBody map[string]any

	// --- model metadata/compat (layer 3) ---

	// MaxTokensField is the body key for the completion budget: "max_tokens" or
	// "max_completion_tokens".
	MaxTokensField string
	// SystemAsContent folds the system prompt into the first user message
	// (text-protocol models with no system role).
	SystemAsContent bool
	// StrictToolsDisabled means this model rejected tool "strict" mode and must
	// not receive it again for this (provider, base_url, model) scope.
	StrictToolsDisabled bool
	// ToolDialect selects the in-band tool-call converter (internal/providers/dialect).
	ToolDialect string

	// --- request context (layer 4) ---

	// Think is the value for the "think" body field when OllamaThink is set.
	Think *bool
	// WhenThinking is a complete alternate object for a thinking request,
	// selected by one condition (see ForThinking).
	WhenThinking *Resolved

	// Schema is the tool-schema normalization profile for this provider.
	Schema SchemaProfile
}

// ForThinking returns the object to use for a request that asks for thinking.
// Thinking-only differences live in WhenThinking, so the request path performs a
// pointer swap instead of re-deriving flags.
func (r *Resolved) ForThinking(on bool) *Resolved {
	if r == nil {
		return nil
	}
	if on && r.WhenThinking != nil {
		return r.WhenThinking
	}
	return r
}

// resolveCount counts Resolve invocations so tests can prove resolution does not
// happen per request.
var resolveCount atomic.Int64

// ResolveCount returns how many times Resolve has been called in this process.
func ResolveCount() int64 { return resolveCount.Load() }

// Resolve builds the compat object for one provider/model.
//
// The same call is used for the provider-level object (model.ID == "") and for
// each catalogued model; a model-scoped object only differs where llm_models
// declares something.
func Resolve(wireAPI, family string, model ModelInfo, s Settings) Resolved {
	resolveCount.Add(1)

	fam := family
	if fam == "" && model.Family != "" {
		fam = model.Family
	}
	if fam == "" {
		fam = InferFamily(wireAPI, s.ProviderType, s.APIBase, s.EndpointFamily)
	}

	// Layers 1 + 2: endpoint family, bundled seeds, operator rows.
	r := defaults()
	r.Family = fam
	applySeeds(&r, wireAPI, fam, model.ID, s.Quirks)
	r.Schema = ProfileFor(schemaName(s))

	// Layer 3: model metadata/compat.
	return ForModel(r, model)
}

// ForModel derives the model-scoped object from a provider-level one. Only the
// model-metadata layer is applied, so a caller that already resolved the
// provider (with its declared family and operator quirks) does not repeat that
// work. This is what the request path uses to warm one cached object per model.
func ForModel(base Resolved, model ModelInfo) Resolved {
	r := base
	r.WhenThinking = nil

	if len(model.Compat) > 0 {
		if f, err := ParseFragment(model.Compat); err == nil {
			f.Apply(&r)
		}
	}
	if field := MaxTokensFieldFor(model.ID); field != "" {
		r.MaxTokensField = field
	}

	// Layer 4 alternate object: one condition — the endpoint carries a thinking
	// policy — produces a complete second object. The thinking object omits the
	// "think" flag entirely so the endpoint's own default applies (which is what
	// the pre-compat code did when a reasoning level was requested).
	if r.OllamaThink {
		if r.Think == nil {
			off := false
			r.Think = &off
		}
		alt := r
		alt.WhenThinking = nil
		alt.Think = nil
		r.WhenThinking = &alt
	}

	return r
}

// schemaName mirrors the transport's schema-provider rule: provider_type wins
// over the (diagnostic only) provider name.
func schemaName(s Settings) string {
	if s.ProviderType != "" {
		return s.ProviderType
	}
	return s.ProviderName
}

// defaults is the family-neutral baseline: an OpenAI-compatible gateway.
func defaults() Resolved {
	return Resolved{
		MaxTokensField:   "max_tokens",
		StreamOptions:    true,
		SupportsThinking: true,
	}
}

// applySeeds merges the bundled seeds and then the operator rows for this
// (wire, family, model). An operator row with Enabled=false suppresses the
// bundled rows it matches, so an operator can turn a family rule off without
// editing the binary.
func applySeeds(r *Resolved, wireAPI, family, model string, operator []Quirk) {
	for _, q := range Bundled() {
		if !q.matches(wireAPI, family, model) {
			continue
		}
		if suppressedBy(q, wireAPI, family, model, operator) {
			continue
		}
		if f, err := ParseFragment(q.Compat); err == nil {
			f.Apply(r)
		}
	}
	for _, q := range operator {
		if !q.Enabled || !q.matches(wireAPI, family, model) {
			continue
		}
		if f, err := ParseFragment(q.Compat); err == nil {
			f.Apply(r)
		}
	}
}

func suppressedBy(bundled Quirk, wireAPI, family, model string, operator []Quirk) bool {
	for _, q := range operator {
		if q.Enabled {
			continue
		}
		if q.WireAPI != "" && q.WireAPI != wireAPI {
			continue
		}
		// A disabled row suppresses the bundled rows it would have overridden:
		// the same family, or the whole wire API when no family is declared.
		if q.EndpointFamily != "" && !strings.EqualFold(q.EndpointFamily, bundled.EndpointFamily) {
			continue
		}
		if !patternMatches(q.ModelPattern, model) {
			continue
		}
		return true
	}
	return false
}

// SummaryKeys lists the compat traits the runtime applies for this object, as
// key names only. Values never appear: extra_body and headers are reported as
// presence markers, so an HTTP surface can expose this read-only without leaking
// an injected body or a header value.
func (r Resolved) SummaryKeys() []string {
	out := make([]string, 0, 16)
	if r.Family != "" {
		out = append(out, "family:"+r.Family)
	}
	if r.SystemAsContent {
		out = append(out, "system_as_content")
	}
	if r.SupportsDeveloperRole {
		out = append(out, "supports_developer_role")
	}
	if r.SupportsStore {
		out = append(out, "supports_store")
	}
	if !r.SupportsThinking {
		out = append(out, "thinking_suppressed")
	}
	if r.StrictToolsDisabled {
		out = append(out, "strict_tools_disabled")
	}
	if r.NativeChatPath != "" {
		out = append(out, "native_chat_path")
	}
	if r.OllamaOptions {
		out = append(out, "ollama_options")
	}
	if r.OllamaThink {
		out = append(out, "ollama_think")
	}
	if r.StreamOptions {
		out = append(out, "stream_options")
	}
	if r.ClampMaxTokens > 0 {
		out = append(out, "clamp_max_tokens")
	}
	if r.SystemCacheControl {
		out = append(out, "system_cache_control")
	}
	if r.ToolPrefixCache {
		out = append(out, "tool_prefix_cache")
	}
	if r.DashScopePassthrough {
		out = append(out, "dashscope_passthrough")
	}
	if r.MaxTokensField != "" {
		out = append(out, "max_tokens_field:"+r.MaxTokensField)
	}
	if r.ToolDialect != "" {
		out = append(out, "tool_dialect:"+r.ToolDialect)
	}
	if len(r.ExtraBody) > 0 {
		out = append(out, "extra_body")
	}
	if len(r.Headers) > 0 {
		out = append(out, "headers")
	}
	sort.Strings(out)
	return out
}

// Fragment is the JSON shape of a compat row (provider_quirks.compat and
// llm_models.compat share it). Pointer fields distinguish "explicitly set" from
// "absent" so merges are deterministic.
type Fragment struct {
	SystemAsContent       *bool             `json:"system_as_content,omitempty"`
	MaxTokensField        *string           `json:"max_tokens_field,omitempty"`
	SupportsDeveloperRole *bool             `json:"supports_developer_role,omitempty"`
	SupportsStore         *bool             `json:"supports_store,omitempty"`
	SupportsThinking      *bool             `json:"supports_thinking,omitempty"`
	StrictToolsDisabled   *bool             `json:"strict_tools_disabled,omitempty"`
	ExtraBody             map[string]any    `json:"extra_body,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	NativeChatPath        *string           `json:"native_chat_path,omitempty"`
	OllamaOptions         *bool             `json:"ollama_options,omitempty"`
	OllamaThink           *bool             `json:"ollama_think,omitempty"`
	StreamOptions         *bool             `json:"stream_options,omitempty"`
	ClampMaxTokens        *int              `json:"clamp_max_tokens,omitempty"`
	SystemCacheControl    *bool             `json:"system_cache_control,omitempty"`
	ToolPrefixCache       *bool             `json:"tool_prefix_cache,omitempty"`
	DashScopePassthrough  *bool             `json:"dashscope_passthrough,omitempty"`
	ToolDialect           *string           `json:"tool_dialect,omitempty"`
}

// ParseFragment decodes a compat JSON fragment. An empty blob is the zero
// fragment.
func ParseFragment(raw json.RawMessage) (Fragment, error) {
	var f Fragment
	if len(raw) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fragment{}, err
	}
	return f, nil
}

// Apply merges the fragment over r.
func (f Fragment) Apply(r *Resolved) {
	if f.SystemAsContent != nil {
		r.SystemAsContent = *f.SystemAsContent
	}
	if f.MaxTokensField != nil {
		r.MaxTokensField = *f.MaxTokensField
	}
	if f.SupportsDeveloperRole != nil {
		r.SupportsDeveloperRole = *f.SupportsDeveloperRole
	}
	if f.SupportsStore != nil {
		r.SupportsStore = *f.SupportsStore
	}
	if f.SupportsThinking != nil {
		r.SupportsThinking = *f.SupportsThinking
	}
	if f.StrictToolsDisabled != nil {
		r.StrictToolsDisabled = *f.StrictToolsDisabled
	}
	if f.ExtraBody != nil {
		if r.ExtraBody == nil {
			r.ExtraBody = make(map[string]any, len(f.ExtraBody))
		}
		for k, v := range f.ExtraBody {
			r.ExtraBody[k] = v
		}
	}
	if f.Headers != nil {
		if r.Headers == nil {
			r.Headers = make(map[string]string, len(f.Headers))
		}
		for k, v := range f.Headers {
			r.Headers[k] = v
		}
	}
	if f.NativeChatPath != nil {
		r.NativeChatPath = *f.NativeChatPath
	}
	if f.OllamaOptions != nil {
		r.OllamaOptions = *f.OllamaOptions
	}
	if f.OllamaThink != nil {
		r.OllamaThink = *f.OllamaThink
	}
	if f.StreamOptions != nil {
		r.StreamOptions = *f.StreamOptions
	}
	if f.ClampMaxTokens != nil {
		r.ClampMaxTokens = *f.ClampMaxTokens
	}
	if f.SystemCacheControl != nil {
		r.SystemCacheControl = *f.SystemCacheControl
	}
	if f.ToolPrefixCache != nil {
		r.ToolPrefixCache = *f.ToolPrefixCache
	}
	if f.DashScopePassthrough != nil {
		r.DashScopePassthrough = *f.DashScopePassthrough
	}
	if f.ToolDialect != nil {
		r.ToolDialect = *f.ToolDialect
	}
}
