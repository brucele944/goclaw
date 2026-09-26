// Package wire is the wire-protocol dispatch registry.
//
// Before this package existed, provider construction and request shaping were
// keyed on the brand (a switch on provider_type in cmd/gateway_providers.go),
// so adding a provider meant editing the registration switch. Here the
// declared llm_providers.wire_api / auth_kind strings are the dispatch key, and
// the vendor-specific base URLs, default models, identity headers and
// provider_type reflection rules live in the brand table (brand.go) as data.
//
// Import direction (enforced by the compiler, not by convention):
//
//	wire -> internal/providers        (transport constructors)
//	internal/store -> wire            (declaration constant aliases)
//	internal/providers must NOT import wire (that would be a cycle).
//
// The constants below are deliberately UNTYPED strings: internal/store aliases
// them (const WireAPIAnthropicMessages = wire.AnthropicMessages) so the store
// layer keeps a single source of truth without conversions at every call site.
package wire

// API is a declared wire protocol (llm_providers.wire_api / llm_models.wire_api).
type API string

// The 8 accepted wire_api values. Keep in sync with migrations/000098 and
// internal/store.ValidWireAPIs (which is derived from this list).
const (
	OpenAICompletions    = "openai-completions"
	OpenAIResponses      = "openai-responses"
	OpenAICodexResponses = "openai-codex-responses"
	AnthropicMessages    = "anthropic-messages"
	GoogleGenerativeAI   = "google-generative-ai"
	GoogleVertex         = "google-vertex"
	OllamaNative         = "ollama-native"
	CLIDelegated         = "cli-delegated"
)

// AuthKind is a declared credential shape (llm_providers.auth_kind).
type AuthKind string

// The 6 accepted auth_kind values.
const (
	AuthAPIKey         = "api_key"
	AuthOAuthBrowser   = "oauth_browser"
	AuthOAuthDevice    = "oauth_device"
	AuthServiceAccount = "service_account"
	AuthCLIDelegated   = "cli_delegated"
	AuthNone           = "none"
)

// apiOrder is the canonical, deterministic enumeration order used by All and by
// error messages ("expected one of ...").
var apiOrder = []API{
	OpenAICompletions,
	OpenAIResponses,
	OpenAICodexResponses,
	AnthropicMessages,
	GoogleGenerativeAI,
	GoogleVertex,
	OllamaNative,
	CLIDelegated,
}

// authKindOrder mirrors apiOrder for auth_kind.
var authKindOrder = []AuthKind{
	AuthAPIKey,
	AuthOAuthBrowser,
	AuthOAuthDevice,
	AuthServiceAccount,
	AuthCLIDelegated,
	AuthNone,
}

// ValidAPIs returns the accepted wire_api values as a lookup set. internal/store
// re-exports this so the store and the dispatcher can never disagree.
func ValidAPIs() map[string]bool {
	m := make(map[string]bool, len(apiOrder))
	for _, a := range apiOrder {
		m[string(a)] = true
	}
	return m
}

// ValidAuthKinds returns the accepted auth_kind values as a lookup set.
func ValidAuthKinds() map[string]bool {
	m := make(map[string]bool, len(authKindOrder))
	for _, a := range authKindOrder {
		m[string(a)] = true
	}
	return m
}

// Valid reports whether a is one of the declared wire protocols.
func (a API) Valid() bool {
	for _, known := range apiOrder {
		if a == known {
			return true
		}
	}
	return false
}

// Valid reports whether a is one of the declared credential shapes.
func (a AuthKind) Valid() bool {
	for _, known := range authKindOrder {
		if a == known {
			return true
		}
	}
	return false
}
