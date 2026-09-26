package providers

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
)

// OpenAIProvider implements Provider for OpenAI-compatible APIs
// (OpenAI, Groq, OpenRouter, DeepSeek, VLLM, etc.)
type OpenAIProvider struct {
	name            string
	apiKey          string
	apiBase         string
	chatPath        string // defaults to "/chat/completions"
	authPrefix      string // auth header prefix, defaults to "Bearer " if empty
	defaultModel    string
	providerType    string            // DB provider_type (e.g. "gemini_native", "openai", "minimax_native")
	siteURL         string            // optional site URL for provider identification (e.g. OpenRouter HTTP-Referer)
	siteTitle       string            // optional site title for provider identification (e.g. OpenRouter X-Title)
	extraHeaders    map[string]string // static headers set on every outgoing request (e.g. fixed User-Agent for kimi_coding)
	client          *http.Client
	retryConfig     RetryConfig
	middlewares     RequestMiddleware // composed middleware chain (nil = no-op)
	registry        ModelRegistry     // model resolution registry (nil = skip)
	noAuthHeader    bool              // when true, doRequest() skips setting Authorization (e.g. Vertex OAuth transport injects its own)
	ollamaNumCtx    *int              // optional Ollama options.num_ctx override (nil = use queried or default value)
	thinkingEnabled *bool             // provider-level override for "think" on Ollama endpoints (nil = default off)
	requestTimeout  time.Duration     // per-provider deadline from settings.timeout_sec (0 = none)
	concurrencyGate ConcurrencyGate   // per-provider max_in_flight gate (nil = unbounded)
	wireAPI         string            // declared wire_api (default "openai-completions")
	endpointFamily  string            // declared endpoint family ("" = infer)
	// compat is the pre-resolved compatibility object for this provider. It is
	// resolved at construction (and by resolvedFor at request time it is only
	// read), never rebuilt per request.
	compat *compat.Resolved
	// modelCompat holds the per-model compat objects: resolved once from
	// llm_models rows by the wire builder, or warmed once on first use for a
	// directly-constructed provider. Read-mostly, so sync.Map.
	modelCompat sync.Map
	// compatQuirks are the operator's declared quirk rows for this wire API.
	compatQuirks []compat.Quirk
	// compatPinned is set by WithCompat: the object is owned by the wire builder
	// (which had the full declaration) and must not be recomputed.
	compatPinned bool
}

func NewOpenAIProvider(name, apiKey, apiBase, defaultModel string) *OpenAIProvider {
	if apiBase == "" {
		apiBase = "https://api.openai.com/v1"
	}
	apiBase = strings.TrimRight(apiBase, "/")

	p := &OpenAIProvider{
		name:         name,
		apiKey:       apiKey,
		apiBase:      apiBase,
		chatPath:     "/chat/completions",
		defaultModel: defaultModel,
		client:       NewDefaultHTTPClient(),
		retryConfig:  DefaultRetryConfig(),
		middlewares:  ComposeMiddlewares(FastModeMiddleware, ServiceTierMiddleware, CacheMiddleware),
		wireAPI:      "openai-completions",
	}
	p.refreshCompat()
	return p
}

// refreshCompat rebuilds the provider-level compat object from the current
// declaration. Called by construction and by the setters that change a resolver
// input; WithCompat pins the object instead.
func (p *OpenAIProvider) refreshCompat() {
	if p.compatPinned {
		return
	}
	r := compat.Resolve(p.wireAPI, "", compat.ModelInfo{}, p.compatSettings())
	p.compat = &r
}

// compatSettings snapshots the resolver inputs. The provider name travels only
// for schema-profile diagnostics; it is never a matching input.
func (p *OpenAIProvider) compatSettings() compat.Settings {
	return compat.Settings{
		ProviderName:   p.name,
		ProviderType:   p.providerType,
		APIBase:        p.apiBase,
		EndpointFamily: p.endpointFamily,
		Quirks:         p.compatQuirks,
	}
}

// WithQuirks pins the operator's quirk rows for this wire API. Bundled seeds
// always apply; these override or suppress them.
func (p *OpenAIProvider) WithQuirks(quirks []compat.Quirk) *OpenAIProvider {
	p.compatQuirks = quirks
	p.refreshCompat()
	return p
}

// WithCompat pins a pre-resolved compat object, typically one the wire builder
// resolved from the full provider declaration plus the operator's quirk rows.
func (p *OpenAIProvider) WithCompat(r *compat.Resolved) *OpenAIProvider {
	if r == nil {
		return p
	}
	p.compat = r
	p.compatPinned = true
	return p
}

// WithModelCompat pins the per-model compat objects resolved once from the
// catalogue rows (one Resolve per model — the catalogue build). A model the
// catalogue does not name is derived from the provider-level object on first use
// and cached.
func (p *OpenAIProvider) WithModelCompat(m map[string]json.RawMessage) *OpenAIProvider {
	for model, raw := range m {
		rc := compat.Resolve(p.wireAPI, p.endpointFamily, compat.ModelInfo{ID: model, Compat: raw}, p.compatSettings())
		p.modelCompat.Store(model, &rc)
	}
	return p
}

// WithWireAPI declares the wire protocol (used only for family inference and the
// resolver's diagnostics).
func (p *OpenAIProvider) WithWireAPI(api string) *OpenAIProvider {
	if api == "" {
		return p
	}
	p.wireAPI = api
	p.refreshCompat()
	return p
}

// WithEndpointFamily declares the endpoint family, skipping inference entirely.
func (p *OpenAIProvider) WithEndpointFamily(family string) *OpenAIProvider {
	p.endpointFamily = family
	p.refreshCompat()
	return p
}

// Compat returns the provider-level resolved compat object (never nil after
// construction).
func (p *OpenAIProvider) Compat() *compat.Resolved { return p.compat }

// resolvedFor returns the pre-resolved object for a model, swapping to the
// thinking alternate when the request asks for reasoning. Lookups only — the
// request path performs no compat allocation.
func (p *OpenAIProvider) resolvedFor(model string, thinking bool) *compat.Resolved {
	return p.compatForModel(model).ForThinking(thinking)
}

// compatForModel returns (and caches) the compat object for a model. The wire
// builder pre-populates the cache from the catalogue; a directly-constructed
// provider warms each model once, on first use, from its provider-level object.
func (p *OpenAIProvider) compatForModel(model string) *compat.Resolved {
	if v, ok := p.modelCompat.Load(model); ok {
		if r, _ := v.(*compat.Resolved); r != nil {
			return r
		}
	}
	if p.compat == nil {
		p.refreshCompat()
	}
	base := p.compat
	if model == "" || base == nil {
		return base
	}
	rc := compat.ForModel(*base, compat.ModelInfo{ID: model})
	actual, _ := p.modelCompat.LoadOrStore(model, &rc)
	r, _ := actual.(*compat.Resolved)
	return r
}

// WithChatPath returns a copy with a custom chat completions path.
func (p *OpenAIProvider) WithChatPath(path string) *OpenAIProvider {
	p.chatPath = path
	return p
}

// WithAuthPrefix sets a custom Authorization header prefix for providers with non-standard auth formats.
// Default is "Bearer " if not set.
func (p *OpenAIProvider) WithAuthPrefix(prefix string) *OpenAIProvider {
	p.authPrefix = prefix
	return p
}

// WithSiteInfo sets site identification headers sent with API requests.
// Used by OpenRouter for rankings (HTTP-Referer, X-Title).
func (p *OpenAIProvider) WithSiteInfo(url, title string) *OpenAIProvider {
	p.siteURL = url
	p.siteTitle = title
	return p
}

// WithExtraHeaders sets static headers attached to every outgoing request.
// Used by providers that require a fixed identity header (e.g. kimi_coding's
// User-Agent: claude-code/0.1.0). Repeat calls merge — keys already present are
// overwritten. Passing an empty map is a no-op.
func (p *OpenAIProvider) WithExtraHeaders(h map[string]string) *OpenAIProvider {
	if len(h) == 0 {
		return p
	}
	if p.extraHeaders == nil {
		p.extraHeaders = make(map[string]string, len(h))
	}
	for k, v := range h {
		p.extraHeaders[k] = v
	}
	return p
}

// ExtraHeaders returns a copy of the static headers configured for this provider.
// Used by adapter_openai.go to mirror the runtime request headers.
func (p *OpenAIProvider) ExtraHeaders() map[string]string {
	if len(p.extraHeaders) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.extraHeaders))
	for k, v := range p.extraHeaders {
		out[k] = v
	}
	return out
}

// WithRegistry sets the model registry for forward-compat resolution.
func (p *OpenAIProvider) WithRegistry(r ModelRegistry) *OpenAIProvider {
	p.registry = r
	return p
}

// WithMiddlewares sets the composed request middleware chain.
func (p *OpenAIProvider) WithMiddlewares(mws ...RequestMiddleware) *OpenAIProvider {
	p.middlewares = ComposeMiddlewares(mws...)
	return p
}

// WithProviderType sets the DB provider_type for correct API endpoint routing in media tools.
func (p *OpenAIProvider) WithProviderType(pt string) *OpenAIProvider {
	p.providerType = pt
	p.refreshCompat()
	return p
}

// WithHTTPClient overrides the default HTTP client. Used by Vertex to inject an oauth2.Transport.
func (p *OpenAIProvider) WithHTTPClient(c *http.Client) *OpenAIProvider {
	if c != nil {
		p.client = c
	}
	return p
}

// WithoutAuthHeader disables the Authorization header in doRequest(). Used by Vertex where
// the oauth2.Transport injects Authorization itself.
func (p *OpenAIProvider) WithoutAuthHeader() *OpenAIProvider {
	p.noAuthHeader = true
	return p
}

// WithOllamaNumCtx sets a static options.num_ctx value injected on every Ollama request.
// When set, this takes precedence over the value queried from /api/show and the built-in
// default of 131072. A non-positive value is ignored.
func (p *OpenAIProvider) WithOllamaNumCtx(n int) *OpenAIProvider {
	if n > 0 {
		p.ollamaNumCtx = &n
	}
	return p
}

// OllamaNumCtx returns the configured num_ctx override, or nil if not set.
func (p *OpenAIProvider) OllamaNumCtx() *int {
	return p.ollamaNumCtx
}

// WithThinkingEnabled sets the provider-level override for whether Ollama
// endpoints should be asked to emit visible reasoning/thinking tokens
// (sets body["think"] in buildRequestBody). nil (not calling this) preserves
// the existing default of disabling thinking on Ollama endpoints.
func (p *OpenAIProvider) WithThinkingEnabled(enabled *bool) *OpenAIProvider {
	p.thinkingEnabled = enabled
	return p
}

// ThinkingEnabled returns the configured provider-level thinking override, or nil if not set.
func (p *OpenAIProvider) ThinkingEnabled() *bool {
	return p.thinkingEnabled
}

func (p *OpenAIProvider) Name() string         { return p.name }
func (p *OpenAIProvider) DefaultModel() string { return p.defaultModel }

// SupportsThinking returns false for endpoints whose compat object suppresses
// reasoning (Ollama models like qwq and deepseek-r1 enable thinking by default
// and goclaw suppresses it unless the operator opts in).
func (p *OpenAIProvider) SupportsThinking() bool {
	return p.compat == nil || p.compat.SupportsThinking
}
func (p *OpenAIProvider) APIKey() string       { return p.apiKey }
func (p *OpenAIProvider) APIBase() string      { return p.apiBase }
func (p *OpenAIProvider) AuthPrefix() string   { return p.authPrefix }
func (p *OpenAIProvider) ProviderType() string { return p.providerType }

// Capabilities implements CapabilitiesAware for pipeline code-path selection.
func (p *OpenAIProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Streaming:        true,
		ToolCalling:      true,
		StreamWithTools:  true,
		Thinking:         p.SupportsThinking(),
		Vision:           true,
		CacheControl:     false,
		MaxContextWindow: 128_000,
		TokenizerID:      "o200k_base",
	}
}

// middlewareConfig builds a MiddlewareConfig from provider fields and the current request.
func (p *OpenAIProvider) middlewareConfig(model string, req ChatRequest) MiddlewareConfig {
	return MiddlewareConfig{
		Provider: p.name,
		Model:    model,
		Caps:     p.Capabilities(),
		AuthType: "api_key",
		APIBase:  p.apiBase,
		Options:  req.Options,
	}
}

// resolveModel returns the model ID to use for a request.
// For OpenRouter, model IDs require a provider prefix (e.g. "anthropic/claude-sonnet-4-5-20250929").
// If the caller passes an unprefixed model, fall back to the provider's default.
// After alias resolution, checks the registry for forward-compat specs.
func (p *OpenAIProvider) resolveModel(model string) string {
	if model == "" {
		return p.defaultModel
	}
	if p.name == "openrouter" && !strings.Contains(model, "/") {
		return p.defaultModel
	}
	// Trigger forward-compat resolution to cache specs for token counting.
	// The model ID itself is unchanged — we don't rename models.
	if p.registry != nil {
		_ = p.registry.Resolve("openai", model)
	}
	return model
}
