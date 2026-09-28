package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers/wire"
)

// Provider type constants.
const (
	ProviderAnthropicNative = "anthropic_native"
	ProviderOpenAICompat    = "openai_compat"
	ProviderGeminiNative    = "gemini_native"
	ProviderOpenRouter      = "openrouter"
	ProviderAIMLAPI         = "aimlapi"
	ProviderGroq            = "groq"
	ProviderDeepSeek        = "deepseek"
	ProviderMistral         = "mistral"
	ProviderXAI             = "xai"
	ProviderMiniMax         = "minimax_native"
	ProviderCohere          = "cohere"
	ProviderPerplexity      = "perplexity"
	ProviderDashScope       = "dashscope"
	ProviderBailian         = "bailian"
	ProviderChatGPTOAuth    = "chatgpt_oauth"
	ProviderClaudeCLI       = "claude_cli"
	ProviderYesScale        = "yescale"
	ProviderZai             = "zai"
	ProviderZaiCoding       = "zai_coding"
	ProviderOllama          = "ollama"          // local or self-hosted Ollama (no API key)
	ProviderOllamaCloud     = "ollama_cloud"    // Ollama Cloud (Bearer token required)
	ProviderACP             = "acp"             // ACP (Agent Client Protocol) agent subprocess
	ProviderNovita          = "novita"          // Novita AI (OpenAI-compatible endpoint)
	ProviderBytePlus        = "byteplus"        // BytePlus ModelArk (Seed 2.0 models)
	ProviderBytePlusCoding  = "byteplus_coding" // BytePlus ModelArk Coding Plan
	ProviderVertex          = "vertex"          // Google Cloud Vertex AI (OAuth2 service account + ADC)
	ProviderKimiCoding      = "kimi_coding"     // Moonshot Kimi Coding (OpenAI-compat, requires fixed User-Agent)
	ProviderAtlasCloud      = "atlascloud"      // Atlas Cloud (OpenAI-compatible endpoint)
	ProviderOpenCode        = "opencode"        // OpenCode Zen gateway (OpenAI-compatible, one OpenCode API key)
	ProviderOpenCodeGo      = "opencode_go"     // OpenCode Go subscription gateway (OpenAI-compatible, same key)

	// MiniMax defaults.
	MiniMaxDefaultAPIBase = "https://api.minimax.io/v1"
	MiniMaxDefaultModel   = "MiniMax-M3"

	// Z.AI defaults.
	ZaiDefaultAPIBase       = "https://api.z.ai/api/paas/v4"
	ZaiCodingDefaultAPIBase = "https://api.z.ai/api/coding/paas/v4"
	ZaiDefaultModel         = "glm-5.2"

	// Novita AI defaults.
	NovitaDefaultAPIBase = "https://api.novita.ai/openai"
	NovitaDefaultModel   = "moonshotai/kimi-k2.5"

	// BytePlus ModelArk defaults.
	BytePlusDefaultAPIBase       = "https://ark.ap-southeast.bytepluses.com/api/v3"
	BytePlusCodingDefaultAPIBase = "https://ark.ap-southeast.bytepluses.com/api/coding/v3"
	BytePlusDefaultModel         = "seed-2-0-lite-260228"

	// Kimi Coding defaults. The upstream requires a fixed User-Agent on every
	// request — handled by the runtime in cmd/gateway_providers.go via
	// OpenAIProvider.WithExtraHeaders.
	KimiCodingDefaultAPIBase    = "https://api.kimi.com/coding/v1"
	KimiCodingDefaultModel      = "kimi-k2-turbo-preview"
	KimiCodingRequiredUserAgent = "claude-code/0.1.0"

	// Atlas Cloud defaults.
	AtlasCloudDefaultAPIBase = "https://api.atlascloud.ai/v1"
	AtlasCloudDefaultModel   = "qwen/qwen3.5-flash"
)

// Vertex AI constants live in internal/providers/vertex.go to avoid a store→providers import cycle
// (store is imported by providers). DB-layer concerns (ProviderVertex type + settings parsing)
// remain in this package.

// ValidProviderTypes lists all accepted provider_type values.
var ValidProviderTypes = map[string]bool{
	ProviderAnthropicNative: true,
	ProviderOpenAICompat:    true,
	ProviderGeminiNative:    true,
	ProviderOpenRouter:      true,
	ProviderAIMLAPI:         true,
	ProviderGroq:            true,
	ProviderDeepSeek:        true,
	ProviderMistral:         true,
	ProviderXAI:             true,
	ProviderMiniMax:         true,
	ProviderCohere:          true,
	ProviderPerplexity:      true,
	ProviderDashScope:       true,
	ProviderBailian:         true,
	ProviderChatGPTOAuth:    true,
	ProviderClaudeCLI:       true,
	ProviderYesScale:        true,
	ProviderZai:             true,
	ProviderZaiCoding:       true,
	ProviderOllama:          true,
	ProviderOllamaCloud:     true,
	ProviderACP:             true,
	ProviderNovita:          true,
	ProviderBytePlus:        true,
	ProviderBytePlusCoding:  true,
	ProviderVertex:          true,
	ProviderKimiCoding:      true,
	ProviderAtlasCloud:      true,
	ProviderOpenCode:        true,
	ProviderOpenCodeGo:      true,
}

// --- Provider declaration: wire protocol + auth shape (provider rework, phase 1) ---
//
// wire_api and auth_kind turn provider behaviour that used to be encoded in Go
// (switches on provider_type, quirks sniffed from URLs) into declared data. The
// allowed values below are the single validation source for the store layer; the
// wire dispatch registry (phase 2) keys off the same strings.

// Wire protocol identifiers accepted by llm_providers.wire_api / llm_models.wire_api.
//
// These are thin aliases of internal/providers/wire, which owns the enum and the
// dispatch registry: the store validates declarations, the wire layer
// dispatches them, and neither can drift from the other.
const (
	WireAPIOpenAICompletions    = wire.OpenAICompletions
	WireAPIOpenAIResponses      = wire.OpenAIResponses
	WireAPIOpenAICodexResponses = wire.OpenAICodexResponses
	WireAPIAnthropicMessages    = wire.AnthropicMessages
	WireAPIGoogleGenerativeAI   = wire.GoogleGenerativeAI
	WireAPIGoogleVertex         = wire.GoogleVertex
	WireAPIOllamaNative         = wire.OllamaNative
	WireAPICLIDelegated         = wire.CLIDelegated
)

// ValidWireAPIs lists all accepted wire_api values. Derived from the registry
// enum, not hand-maintained.
var ValidWireAPIs = wire.ValidAPIs()

// Auth shape identifiers accepted by llm_providers.auth_kind.
const (
	AuthKindAPIKey         = wire.AuthAPIKey
	AuthKindOAuthBrowser   = wire.AuthOAuthBrowser
	AuthKindOAuthDevice    = wire.AuthOAuthDevice
	AuthKindServiceAccount = wire.AuthServiceAccount
	AuthKindCLIDelegated   = wire.AuthCLIDelegated
	AuthKindNone           = wire.AuthNone
)

// ValidAuthKinds lists all accepted auth_kind values. Derived from the registry
// enum, not hand-maintained.
var ValidAuthKinds = wire.ValidAuthKinds()

// CurrentSettingsVersion is the provider settings JSONB schema version this build
// writes and understands. Bump it (and add the matching decoder) whenever the
// meaning of keys inside llm_providers.settings changes incompatibly.
const CurrentSettingsVersion = 1

// sortedKeys returns the map's keys in deterministic order for error messages.
func sortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// ValidateWireAPI rejects wire_api values the wire layer cannot dispatch.
// The error message is i18n-keyed (see internal/i18n keys).
func ValidateWireAPI(wireAPI string) error {
	if ValidWireAPIs[wireAPI] {
		return nil
	}
	return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidWireAPI, wireAPI, sortedKeys(ValidWireAPIs)))
}

// ValidateAuthKind rejects auth_kind values the credential layer cannot satisfy.
func ValidateAuthKind(authKind string) error {
	if ValidAuthKinds[authKind] {
		return nil
	}
	return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidAuthKind, authKind, sortedKeys(ValidAuthKinds)))
}

// ValidateSettingsVersion rejects settings blob versions this build cannot decode.
// Versions above CurrentSettingsVersion were written by a newer binary; version 0
// means "unset" and must be defaulted by the caller (see NormalizeProviderDeclaration).
func ValidateSettingsVersion(version int) error {
	if version >= 1 && version <= CurrentSettingsVersion {
		return nil
	}
	return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidSettingsVersion, version, CurrentSettingsVersion))
}

// NormalizeProviderDeclaration fills empty declaration fields from the provider
// type's declared brand and validates them, returning the first i18n-keyed error.
//
// Deriving beats defaulting: an empty wire_api means "the caller did not say",
// not "OpenAI-compatible". A row created through any surface (HTTP, MCP,
// onboarding, OAuth) therefore gets the transport its brand declares, instead of
// an anthropic/ollama/CLI/ChatGPT row being built by the OpenAI transport. A
// provider type with no brand is rejected rather than silently downgraded.
func NormalizeProviderDeclaration(p *LLMProviderData) error {
	if p.WireAPI == "" {
		api, ok := wire.APIForBrand(p.ProviderType)
		if !ok {
			return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderUnsupportedType, p.ProviderType, strings.Join(wire.BrandProviderTypes(), ", ")))
		}
		p.WireAPI = string(api)
	}
	if p.AuthKind == "" {
		p.AuthKind = defaultAuthKind(p.WireAPI)
	}
	if p.SettingsVersion == 0 {
		p.SettingsVersion = CurrentSettingsVersion
	}
	if err := ValidateWireAPI(p.WireAPI); err != nil {
		return err
	}
	if err := ValidateAuthKind(p.AuthKind); err != nil {
		return err
	}
	return ValidateSettingsVersion(p.SettingsVersion)
}

// defaultAuthKind returns the credential source a wire protocol implies when the
// row does not declare one: subprocess transports authenticate by delegation,
// Ollama needs no credential, the ChatGPT codex API is browser OAuth, and every
// other wire protocol takes an API key.
func defaultAuthKind(wireAPI string) string {
	if d, ok := wire.Lookup(wire.API(wireAPI)); ok && d.AuthKind != "" {
		return string(d.AuthKind)
	}
	return AuthKindAPIKey
}

// ValidateProviderUpdates normalizes and validates declaration columns inside a
// dynamic update map before it reaches the DB. Keys are optional: absent keys are
// left alone, except that changing provider_type to a known brand without stating
// wire_api/auth_kind fills both from that brand, so repointing a row cannot leave
// it on the previous transport's declaration.
func ValidateProviderUpdates(updates map[string]any) error {
	if raw, ok := updates["provider_type"]; ok {
		if providerType, ok := raw.(string); ok {
			if api, known := wire.APIForBrand(providerType); known {
				if _, stated := updates["wire_api"]; !stated {
					updates["wire_api"] = string(api)
				}
				if _, stated := updates["auth_kind"]; !stated {
					updates["auth_kind"] = defaultAuthKind(string(api))
				}
			}
		}
	}
	if raw, ok := updates["wire_api"]; ok {
		wireAPI, ok := raw.(string)
		if !ok {
			return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidWireAPI, raw, sortedKeys(ValidWireAPIs)))
		}
		if err := ValidateWireAPI(wireAPI); err != nil {
			return err
		}
	}
	if raw, ok := updates["auth_kind"]; ok {
		authKind, ok := raw.(string)
		if !ok {
			return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidAuthKind, raw, sortedKeys(ValidAuthKinds)))
		}
		if err := ValidateAuthKind(authKind); err != nil {
			return err
		}
	}
	if raw, ok := updates["settings_version"]; ok {
		version, ok := integerValue(raw)
		if !ok {
			return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderInvalidSettingsVersion, raw, CurrentSettingsVersion))
		}
		if err := ValidateSettingsVersion(version); err != nil {
			return err
		}
	}
	return nil
}

// integerValue coerces the numeric types a JSON- or Go-built update map may carry.
func integerValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// VertexProviderSettings holds Vertex-specific config stored in llm_providers.settings JSONB.
type VertexProviderSettings struct {
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`
	Model     string `json:"model,omitempty"` // optional default model override (e.g. "google/gemini-2.5-pro-001")
}

// ParseVertexProviderSettings extracts Vertex config from settings JSONB.
// Returns nil if project_id or region is missing (both required).
func ParseVertexProviderSettings(settings json.RawMessage) *VertexProviderSettings {
	if len(settings) == 0 {
		return nil
	}
	var s VertexProviderSettings
	if json.Unmarshal(settings, &s) != nil {
		return nil
	}
	if s.ProjectID == "" || s.Region == "" {
		return nil
	}
	return &s
}

// LLMProviderData represents an LLM provider configuration.
type LLMProviderData struct {
	BaseModel
	TenantID     uuid.UUID       `json:"tenant_id,omitempty" db:"tenant_id"`
	Name         string          `json:"name" db:"name"`
	DisplayName  string          `json:"display_name,omitempty" db:"display_name"`
	ProviderType string          `json:"provider_type" db:"provider_type"`
	APIBase      string          `json:"api_base,omitempty" db:"api_base"`
	APIKey       string          `json:"api_key,omitempty" db:"api_key"`
	Enabled      bool            `json:"enabled" db:"enabled"`
	Settings     json.RawMessage `json:"settings,omitempty" db:"settings"`
	// Declaration columns (provider rework, phase 1). WireAPI/AuthKind are the
	// declared wire protocol and credential shape; ExecPath carries the
	// executable for cli-delegated providers (api_base stays authoritative for
	// one release, dual-read); SettingsVersion versions the Settings JSONB.
	WireAPI         string `json:"wire_api" db:"wire_api"`
	AuthKind        string `json:"auth_kind" db:"auth_kind"`
	ExecPath        string `json:"exec_path,omitempty" db:"exec_path"`
	SettingsVersion int    `json:"settings_version" db:"settings_version"`
}

// LLMModel is a per-provider model declaration. Scope is inherited through
// ProviderID: a model row is visible exactly where its provider is visible.
// Nullable metadata columns map to pointers so "unknown" stays distinct from
// zero (a 0 cost or a 0 context window is not a real value).
type LLMModel struct {
	BaseModel
	ProviderID        uuid.UUID       `json:"provider_id" db:"provider_id"`
	ModelID           string          `json:"model_id" db:"model_id"`
	DisplayName       *string         `json:"display_name,omitempty" db:"display_name"`
	WireAPI           *string         `json:"wire_api,omitempty" db:"wire_api"`
	ContextWindow     *int            `json:"context_window,omitempty" db:"context_window"`
	MaxTokens         *int            `json:"max_tokens,omitempty" db:"max_tokens"`
	MaxContextWindow  *int            `json:"max_context_window,omitempty" db:"max_context_window"`
	CostInput         *float64        `json:"cost_input,omitempty" db:"cost_input"`
	CostOutput        *float64        `json:"cost_output,omitempty" db:"cost_output"`
	CostCacheRead     *float64        `json:"cost_cache_read,omitempty" db:"cost_cache_read"`
	CostCacheWrite    *float64        `json:"cost_cache_write,omitempty" db:"cost_cache_write"`
	Modalities        json.RawMessage `json:"modalities,omitempty" db:"modalities"`
	Capabilities      json.RawMessage `json:"capabilities,omitempty" db:"capabilities"`
	Reasoning         json.RawMessage `json:"reasoning,omitempty" db:"reasoning"`
	Tokenizer         *string         `json:"tokenizer,omitempty" db:"tokenizer"`
	Compat            json.RawMessage `json:"compat,omitempty" db:"compat"`
	Source            string          `json:"source" db:"source"`
	Authoritative     bool            `json:"authoritative" db:"authoritative"`
	FetchedAt         *time.Time      `json:"fetched_at,omitempty" db:"fetched_at"`
	StaticFingerprint *string         `json:"static_fingerprint,omitempty" db:"static_fingerprint"`
	Enabled           bool            `json:"enabled" db:"enabled"`
}

// ProviderQuirk is a declared compatibility rule. A row with a nil TenantID is
// bundled/global (shipped with the binary, the api_keys precedent) and applies
// to every tenant; a tenant row overrides it.
type ProviderQuirk struct {
	BaseModel
	TenantID       *uuid.UUID      `json:"tenant_id,omitempty" db:"tenant_id"`
	WireAPI        string          `json:"wire_api" db:"wire_api"`
	EndpointFamily *string         `json:"endpoint_family,omitempty" db:"endpoint_family"`
	ModelPattern   *string         `json:"model_pattern,omitempty" db:"model_pattern"`
	Compat         json.RawMessage `json:"compat,omitempty" db:"compat"`
	Note           *string         `json:"note,omitempty" db:"note"`
	Source         string          `json:"source" db:"source"`
	Enabled        bool            `json:"enabled" db:"enabled"`
}

// RequiredMemoryEmbeddingDimensions is the fixed vector size used by the pgvector memory schema.
// All memory embeddings must match this dimensionality until the schema supports variable sizes.
const RequiredMemoryEmbeddingDimensions = 1536

// EmbeddingSettings holds embedding-specific configuration stored in provider settings JSONB.
type EmbeddingSettings struct {
	Enabled    bool   `json:"enabled" db:"-"`
	Model      string `json:"model,omitempty" db:"-"`      // e.g. "text-embedding-3-small"
	APIBase    string `json:"api_base,omitempty" db:"-"`   // override if embedding endpoint differs from chat
	Dimensions int    `json:"dimensions,omitempty" db:"-"` // truncate output to N dims (e.g. 1536); 0 = model default
}

// ProviderReasoningConfig holds provider-owned default reasoning settings.
// These defaults are inherited by agents unless they save a custom override.
type ProviderReasoningConfig struct {
	Effort   string `json:"effort,omitempty" db:"-"`
	Fallback string `json:"fallback,omitempty" db:"-"`
}

// OllamaSettings holds Ollama-specific configuration stored in the provider settings JSONB.
type OllamaSettings struct {
	// NumCtx overrides the context window size sent in options.num_ctx on every request.
	// When nil, the gateway queries the Ollama API (/api/show) for the model's native
	// context length, falling back to 131072 if the API is unreachable.
	NumCtx *int `json:"num_ctx,omitempty" db:"-"`
}

// ParseOllamaSettings extracts Ollama-specific config from a provider's settings JSONB.
// Returns nil when no relevant settings are present.
func ParseOllamaSettings(settings json.RawMessage) *OllamaSettings {
	if len(settings) == 0 {
		return nil
	}
	var s OllamaSettings
	if json.Unmarshal(settings, &s) != nil || s.NumCtx == nil {
		return nil
	}
	return &s
}

// ChatGPTOAuthProviderSettings holds provider-level defaults for Codex account pooling.
type ChatGPTOAuthProviderSettings struct {
	CodexPool *ChatGPTOAuthRoutingConfig `json:"codex_pool,omitempty" db:"-"`
}

// ParseEmbeddingSettings extracts embedding config from a provider's settings JSONB.
// Returns nil if not configured.
func ParseEmbeddingSettings(settings json.RawMessage) *EmbeddingSettings {
	if len(settings) == 0 {
		return nil
	}
	var s struct {
		Embedding *EmbeddingSettings `json:"embedding"`
	}
	if json.Unmarshal(settings, &s) != nil || s.Embedding == nil {
		return nil
	}
	return s.Embedding
}

// ParseThinkingEnabled extracts the provider-level override for whether the
// provider should be asked to emit visible reasoning/thinking tokens (e.g.
// Ollama native "think" field, OpenAI-compat "think" for Ollama endpoints).
// Returns nil when unset in settings JSONB, meaning "use provider default"
// (currently off for Ollama). Explicit true/false overrides that default.
func ParseThinkingEnabled(settings json.RawMessage) *bool {
	if len(settings) == 0 {
		return nil
	}
	var s struct {
		ThinkingEnabled *bool `json:"thinking_enabled"`
	}
	if json.Unmarshal(settings, &s) != nil {
		return nil
	}
	return s.ThinkingEnabled
}

// ParseChatGPTOAuthProviderSettings extracts provider-level Codex pool defaults from settings JSONB.
func ParseChatGPTOAuthProviderSettings(settings json.RawMessage) *ChatGPTOAuthProviderSettings {
	if len(settings) == 0 {
		return nil
	}
	var s ChatGPTOAuthProviderSettings
	if json.Unmarshal(settings, &s) != nil {
		return nil
	}
	s.CodexPool = normalizeChatGPTOAuthRoutingConfig(s.CodexPool)
	if s.CodexPool == nil {
		return nil
	}
	s.CodexPool.OverrideMode = ""
	return &s
}

// ParseProviderReasoningConfig extracts provider-owned reasoning defaults from settings JSONB.
// Returns nil when no non-default provider reasoning is configured.
func ParseProviderReasoningConfig(settings json.RawMessage) *ProviderReasoningConfig {
	if len(settings) == 0 {
		return nil
	}
	var raw struct {
		ReasoningDefaults *ProviderReasoningConfig `json:"reasoning_defaults"`
	}
	if json.Unmarshal(settings, &raw) != nil {
		return nil
	}
	return normalizeProviderReasoningConfig(raw.ReasoningDefaults)
}

func normalizeProviderReasoningConfig(raw *ProviderReasoningConfig) *ProviderReasoningConfig {
	if raw == nil {
		return nil
	}
	cfg := &ProviderReasoningConfig{
		Effort:   normalizeReasoningEffort(raw.Effort),
		Fallback: normalizeReasoningFallback(raw.Fallback),
	}
	if cfg.Effort == "" {
		cfg.Effort = "off"
	}
	if cfg.Effort == "off" && cfg.Fallback == ReasoningFallbackDowngrade {
		return nil
	}
	return cfg
}

// NoEmbeddingTypes lists provider types that cannot serve embeddings.
var NoEmbeddingTypes = map[string]bool{
	ProviderAnthropicNative: true, // uses x-api-key auth, not Bearer; no embedding models
	ProviderACP:             true,
	ProviderClaudeCLI:       true,
	ProviderChatGPTOAuth:    true,
	ProviderVertex:          true, // Vertex embeddings live on a different native endpoint, not on /endpoints/openapi
}

// ProviderColumns is the canonical llm_providers column list in scan order
// (declaration columns included). Both backends select these names, so adding a
// column means touching this constant once instead of every query.
//
// The nullable text columns are coalesced because the matching domain fields are
// plain strings and a NULL scan into a string fails: display_name / api_base /
// api_key have been nullable since migration 000001 (rows written by direct SQL
// or by the migration-time seeds carry NULL), and migration 000098 leaves
// exec_path NULL for every provider that is not cli-delegated.
const ProviderColumns = "id, name, COALESCE(display_name, '') AS display_name, provider_type, COALESCE(api_base, '') AS api_base, COALESCE(api_key, '') AS api_key, enabled, settings, wire_api, auth_kind, COALESCE(exec_path, '') AS exec_path, settings_version, created_at, updated_at, tenant_id"

// LLMModelColumns is the canonical llm_models column list in scan order.
const LLMModelColumns = "id, provider_id, model_id, display_name, wire_api, context_window, max_tokens, max_context_window, cost_input, cost_output, cost_cache_read, cost_cache_write, modalities, capabilities, reasoning, tokenizer, compat, source, authoritative, fetched_at, static_fingerprint, enabled, created_at, updated_at"

// QualifiedModelColumns returns LLMModelColumns with every column prefixed by
// alias, for joins that need unambiguous names.
// SECURITY: alias must be a hardcoded literal — it is interpolated into SQL.
func QualifiedModelColumns(alias string) string {
	parts := strings.Split(LLMModelColumns, ", ")
	for i, part := range parts {
		parts[i] = alias + "." + part
	}
	return strings.Join(parts, ", ")
}

// Model source values (llm_models.source).
const (
	ModelSourceBundled    = "bundled"
	ModelSourceDiscovered = "discovered"
	ModelSourceOperator   = "operator"
)

// FillModelDefaults fills the NOT NULL JSON/text columns of a model row with the
// same values the DDL defaults use, so both backends insert identical rows
// regardless of whether the caller set them.
func FillModelDefaults(m *LLMModel) {
	if len(m.Modalities) == 0 {
		m.Modalities = json.RawMessage(`["text"]`)
	}
	if len(m.Capabilities) == 0 {
		m.Capabilities = json.RawMessage(`{}`)
	}
	if len(m.Reasoning) == 0 {
		m.Reasoning = json.RawMessage(`{}`)
	}
	if len(m.Compat) == 0 {
		m.Compat = json.RawMessage(`{}`)
	}
	if m.Source == "" {
		m.Source = ModelSourceBundled
	}
}

// ErrProviderNotFound returns the i18n-keyed "provider not found" error used by
// the model-write tenant guard. A cross-tenant attempt deliberately reports the
// same error as a missing provider so IDs cannot be probed across tenants.
func ErrProviderNotFound(providerID uuid.UUID) error {
	return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderNotFound, providerID.String()))
}

// ErrProviderModelNotFound returns the i18n-keyed "model not found" error.
func ErrProviderModelNotFound(providerID uuid.UUID, modelID string) error {
	return errors.New(i18n.T(i18n.DefaultLocale, i18n.MsgProviderModelNotFound, modelID, providerID.String()))
}

// --- Provider health: durable cooldown state (provider rework, phase 5) ---

// ErrorClassUnknown is the histogram bucket for a failure the classifier could
// not name. Keeping the bucket means a failure is never silently dropped from
// the histogram, and keeps the bucket key SQL-safe (see NormalizeErrorClass).
const ErrorClassUnknown = "unknown"

// ProviderHealth is the durable circuit-breaker state of one provider, stored in
// provider_health + provider_error_counts (migration 000100). The fallback
// wrapper writes it through providers.CooldownStore and reads it back on every
// process start, so a gateway restart no longer forgets that a provider is
// cooling down.
//
// A provider that never failed has no row: callers get a zero ProviderHealth
// (see NewProviderHealth) rather than an error.
type ProviderHealth struct {
	ProviderID          uuid.UUID  `json:"provider_id" db:"provider_id"`
	ConsecutiveFailures int        `json:"consecutive_failures" db:"consecutive_failures"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty" db:"cooldown_until"`
	LastErrorClass      string     `json:"last_error_class,omitempty" db:"last_error_class"`
	LastProbeAt         *time.Time `json:"last_probe_at,omitempty" db:"last_probe_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
	// ErrorCounts is the per-error-class histogram from provider_error_counts,
	// never nil for a value returned by the stores.
	ErrorCounts map[string]int `json:"error_counts" db:"-"`
}

// NewProviderHealth returns the health of a provider that never failed: no
// cooldown, no failures, empty histogram.
func NewProviderHealth(providerID uuid.UUID) *ProviderHealth {
	return &ProviderHealth{ProviderID: providerID, ErrorCounts: map[string]int{}}
}

// CoolingDown reports whether the persisted cooldown is still active at now.
func (h *ProviderHealth) CoolingDown(now time.Time) bool {
	return h != nil && h.CooldownUntil != nil && now.Before(*h.CooldownUntil)
}

// NormalizeErrorClass maps an error class onto a safe histogram bucket key. The
// key is bound as a SQL parameter, but it is also the bucket identity reported
// by the health surface, so anything outside [a-z0-9_] collapses to
// ErrorClassUnknown instead of leaking an arbitrary classifier string.
func NormalizeErrorClass(class string) string {
	class = strings.ToLower(strings.TrimSpace(class))
	if class == "" {
		return ErrorClassUnknown
	}
	for _, r := range class {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return ErrorClassUnknown
		}
	}
	return class
}

// ProviderFallbackChainKey is the llm_providers.settings key holding the
// provider-level default fallback chain. It is appended after an agent's own
// model_fallback chain when the runtime provider is resolved (the agent wins on
// conflict) — see MergeFallbackCandidates.
const ProviderFallbackChainKey = "fallback_chain"

// providerChainEntry parses one fallback_chain entry. Both shapes are accepted
// because the key is operator-editable JSON: {"provider":..,"model":..} or the
// compact "provider/model" (phase 5 documents the chain as ordered
// `provider/model`). Only the first "/" splits, so vendor-prefixed model ids
// ("openrouter/google/gemini-2.5-pro") survive.
type providerChainEntry ModelFallbackCandidate

func (e *providerChainEntry) UnmarshalJSON(data []byte) error {
	var asObject ModelFallbackCandidate
	if err := json.Unmarshal(data, &asObject); err == nil {
		// An incomplete object is kept as-is and dropped by
		// ParseProviderFallbackChain, so one typo cannot disable the whole chain.
		*e = providerChainEntry(asObject)
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err != nil {
		return fmt.Errorf("fallback_chain entry must be {provider, model} or \"provider/model\": %s", data)
	}
	provider, model, ok := strings.Cut(asString, "/")
	if !ok || provider == "" || model == "" {
		// No usable provider/model split: keep it empty and let the parser drop it.
		*e = providerChainEntry(ModelFallbackCandidate{})
		return nil
	}
	*e = providerChainEntry(ModelFallbackCandidate{Provider: provider, Model: model})
	return nil
}

// ParseProviderFallbackChain extracts the provider-level default fallback chain
// from llm_providers.settings. A malformed chain yields nil rather than an
// error: a bad settings blob must not break provider resolution for the whole
// agent, it just means "no provider-level chain".
func ParseProviderFallbackChain(settings json.RawMessage) []ModelFallbackCandidate {
	if len(settings) == 0 {
		return nil
	}
	var raw struct {
		FallbackChain []providerChainEntry `json:"fallback_chain"`
	}
	if json.Unmarshal(settings, &raw) != nil {
		return nil
	}
	out := make([]ModelFallbackCandidate, 0, len(raw.FallbackChain))
	for _, entry := range raw.FallbackChain {
		candidate := ModelFallbackCandidate{
			Provider: strings.TrimSpace(entry.Provider),
			Model:    strings.TrimSpace(entry.Model),
		}
		if candidate.Provider == "" || candidate.Model == "" {
			continue
		}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeFallbackCandidates merges an agent's own chain with the provider-level
// default chain: agent candidates keep their priority and the provider chain is
// appended after them, minus the pairs the agent already named. Dedup is by the
// exact (provider, model) pair, so declare pairs verbatim to avoid duplicates.
func MergeFallbackCandidates(agentChain, providerChain []ModelFallbackCandidate) []ModelFallbackCandidate {
	if len(agentChain) == 0 && len(providerChain) == 0 {
		return nil
	}
	seen := make(map[ModelFallbackCandidate]bool, len(agentChain)+len(providerChain))
	out := make([]ModelFallbackCandidate, 0, len(agentChain)+len(providerChain))
	for _, chain := range [][]ModelFallbackCandidate{agentChain, providerChain} {
		for _, candidate := range chain {
			if candidate.Provider == "" || candidate.Model == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ProviderQuirkStore is the optional write slice for provider_quirks. It is kept
// out of ProviderStore deliberately: seeding needs it, most narrow test fakes do
// not, and callers type-assert instead of every implementer growing a method.
type ProviderQuirkStore interface {
	UpsertQuirks(ctx context.Context, quirks []ProviderQuirk) error
}

// ProviderStore manages LLM providers.
type ProviderStore interface {
	CreateProvider(ctx context.Context, p *LLMProviderData) error
	GetProvider(ctx context.Context, id uuid.UUID) (*LLMProviderData, error)
	GetProviderByName(ctx context.Context, name string) (*LLMProviderData, error)
	ListProviders(ctx context.Context) ([]LLMProviderData, error)
	ListAllProviders(ctx context.Context) ([]LLMProviderData, error)
	UpdateProvider(ctx context.Context, id uuid.UUID, updates map[string]any) error
	DeleteProvider(ctx context.Context, id uuid.UUID) error

	// ListModels returns the declared models of one provider, scoped to the
	// caller's tenant (a model is visible exactly where its provider is).
	ListModels(ctx context.Context, providerID uuid.UUID) ([]LLMModel, error)
	// UpsertModels inserts or refreshes model rows, idempotent on
	// (provider_id, model_id). The parent provider's tenant is verified first.
	// On conflict the metadata is refreshed but the operator's `enabled` flag is
	// preserved (toggle via SetModelEnabled).
	UpsertModels(ctx context.Context, providerID uuid.UUID, models []LLMModel) error
	// SetModelEnabled flips the enabled flag of a single model row, after
	// verifying the parent provider's tenant.
	SetModelEnabled(ctx context.Context, providerID uuid.UUID, modelID string, enabled bool) error
	// ListQuirks returns enabled quirks for a wire protocol: bundled rows
	// (tenant_id IS NULL) plus the caller's tenant rows, tenant rows first.
	ListQuirks(ctx context.Context, wireAPI string) ([]ProviderQuirk, error)

	// GetProviderHealth returns the durable cooldown/error state of one
	// provider. A provider that never failed has no row and yields a zero
	// ProviderHealth, not an error.
	GetProviderHealth(ctx context.Context, providerID uuid.UUID) (*ProviderHealth, error)
	// RecordProviderFailure bumps the consecutive-failure count and the
	// error-class histogram, and stores the new cooldown deadline.
	// errorClass is normalized by the store (NormalizeErrorClass).
	RecordProviderFailure(ctx context.Context, providerID uuid.UUID, errorClass string, cooldownUntil time.Time) error
	// RecordProviderSuccess clears cooldown state and the failure count; the
	// error-class histogram is retained as history.
	RecordProviderSuccess(ctx context.Context, providerID uuid.UUID) error
	// MarkProviderProbe stamps last_probe_at without touching cooldown state, so
	// the health surface can tell "no traffic" apart from "not re-checked".
	MarkProviderProbe(ctx context.Context, providerID uuid.UUID) error
	// ResetProviderHealth clears cooldown state, failure count and histogram —
	// the operator's manual escape hatch (goclaw providers health --reset).
	ResetProviderHealth(ctx context.Context, providerID uuid.UUID) error
}
