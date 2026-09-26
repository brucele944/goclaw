package providers

import "github.com/nextlevelbuilder/goclaw/internal/providers/compat"

// ProviderCapabilities declares what a provider supports.
// Queried by pipeline to choose code paths (streaming vs non-streaming, etc.)
type ProviderCapabilities struct {
	Streaming        bool   // supports ChatStream()
	ToolCalling      bool   // supports tools in request
	StreamWithTools  bool   // can stream while tool calls are in-flight
	Thinking         bool   // supports extended thinking / reasoning
	Vision           bool   // supports image inputs
	CacheControl     bool   // supports provider prompt cache controls
	ImageGeneration  bool   // supports native image_generation tool (Codex/OpenAI Responses API)
	MaxContextWindow int    // default context window for default model
	TokenizerID      string // for tokencount package mapping
}

// CapabilitiesAware is optionally implemented by Provider.
// Pipeline checks this to choose code path.
type CapabilitiesAware interface {
	Capabilities() ProviderCapabilities
}

// ProviderTypeAware is implemented by transports that know their declared
// provider_type. The request path keys the model catalogue by it (the shipped
// snapshot, the discovery rows and llm_models.provider_type all share that key),
// so a capability lookup never has to inspect the user-chosen provider name.
type ProviderTypeAware interface {
	ProviderType() string
}

// ResolvedCompatAware is implemented by transports that hold a phase-4 resolved
// compat object. The request path reads it to answer cache questions the
// provider-level capability flags cannot express (a wire that declares cache
// breakpoint support for one endpoint family).
type ResolvedCompatAware interface {
	Compat() *compat.Resolved
}

// ModelCapabilityOverride carries the capability keys one catalogue row
// (llm_models.capabilities + llm_models.max_context_window) declares. A nil bool
// field means "the row does not mention it — inherit the provider's own
// Capabilities()"; MaxContextWindow of 0 likewise means "no per-model window".
//
// The JSON keys the catalogue writes are tool_calling, vision,
// stream_with_tools, cache_control and max_context_window.
type ModelCapabilityOverride struct {
	ToolCalling      *bool
	Vision           *bool
	StreamWithTools  *bool
	CacheControl     *bool
	MaxContextWindow int
}

// ModelCapabilityLookup resolves the declared override for one catalogue row.
// It is keyed by provider name, provider type and model id — the shipped
// snapshot implementation uses the type, a store-backed one can use the name —
// and is deliberately context-free, mirroring ModelCostResolver: it is installed
// once, never resolved per request. ok=false means the row is not catalogued or
// declares nothing this path consumes.
type ModelCapabilityLookup func(providerName, providerType, model string) (ModelCapabilityOverride, bool)

// ModelCapabilityResolution is the capability state that governs one request:
// the provider's declared capabilities with the catalogue row's per-model
// override already applied, plus the row's declared context window.
type ModelCapabilityResolution struct {
	// ProviderDeclared is false when the provider does not implement
	// CapabilitiesAware. Consumers MUST NOT gate on the zero values in that case
	// — "undeclared" is not "unsupported".
	ProviderDeclared bool
	// Capabilities are the effective capabilities for this request.
	Capabilities ProviderCapabilities
	// ContextWindowClamp is the window the catalogue row declares for this model
	// (0 = the row declares none). It is a clamp, never a raise: consumers take
	// min(agentWindow, ContextWindowClamp).
	ContextWindowClamp int
}

// EffectiveCapabilities overlays a per-model override onto the provider's
// declared default capabilities. Only fields explicitly set on override
// replace base; everything else (Streaming, Thinking, ImageGeneration,
// TokenizerID, MaxContextWindow and any capability the model row doesn't
// mention) is inherited unchanged from base — the row's window travels in
// ModelCapabilityResolution.ContextWindowClamp instead, so a per-model clamp is
// never confused with the provider's own default window.
func EffectiveCapabilities(base ProviderCapabilities, override ModelCapabilityOverride) ProviderCapabilities {
	out := base
	if override.ToolCalling != nil {
		out.ToolCalling = *override.ToolCalling
	}
	if override.Vision != nil {
		out.Vision = *override.Vision
	}
	if override.StreamWithTools != nil {
		out.StreamWithTools = *override.StreamWithTools
	}
	if override.CacheControl != nil {
		out.CacheControl = *override.CacheControl
	}
	return out
}

// ResolveModelCapabilities builds the resolution for one request: the
// provider's declared capabilities overlaid with the catalogue row's override.
// A nil lookup, an uncatalogued row, or a row that declares nothing leaves the
// provider's capabilities and the window clamp untouched.
func ResolveModelCapabilities(base ProviderCapabilities, lookup ModelCapabilityLookup, providerName, providerType, model string) ModelCapabilityResolution {
	res := ModelCapabilityResolution{ProviderDeclared: true, Capabilities: base}
	if lookup == nil {
		return res
	}
	override, ok := lookup(providerName, providerType, model)
	if !ok {
		return res
	}
	res.Capabilities = EffectiveCapabilities(base, override)
	res.ContextWindowClamp = override.MaxContextWindow
	return res
}

// CacheBreakpointsSupported reports whether OpenAI-style prompt-cache parameters
// (prompt_cache_key / prompt_cache_retention) should be attached to this
// provider's requests. Two declarations can say yes: the effective capability
// flag (per-model row or provider default) and the phase-4 resolved compat
// object, which declares cache support per endpoint family for wires whose
// caching is expressed as transport-level cache_control breakpoints rather than
// capabilities. Anthropic's own block-level cache_control is applied by its
// transport and is unaffected by this predicate.
func CacheBreakpointsSupported(p Provider, caps ProviderCapabilities) bool {
	if caps.CacheControl {
		return true
	}
	if ca, ok := p.(ResolvedCompatAware); ok {
		if r := ca.Compat(); r != nil && (r.SystemCacheControl || r.ToolPrefixCache) {
			return true
		}
	}
	return false
}
