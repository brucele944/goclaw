package wire

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Reflect says whether a brand's provider_type is passed to the transport's
// WithProviderType. That value is behaviour, not bookkeeping: the OpenAI-compat
// transport keys schema normalisation (gemini/kimi_coding), DashScope
// cache_control wrapping, Ollama think/num_ctx injection, and Together
// extension suppression off it. The two registration call sites historically
// disagreed, so both rules are recorded per brand and reproduced exactly.
type Reflect int

const (
	// ReflectNever: neither call site passes provider_type.
	ReflectNever Reflect = iota
	// ReflectConfigAndDB: both the config-file and the DB registration path pass it.
	ReflectConfigAndDB
	// ReflectDBOnly: only the DB registration path passes it.
	ReflectDBOnly
)

// brand constructors. The zero value is the plain OpenAI-compatible provider.
const (
	constructorOpenAI    = ""
	constructorDashScope = "dashscope"
	constructorAIMLAPI   = "aimlapi"
)

// CLI kinds for the cli-delegated wire API. Both brands share one wire protocol
// but a different subprocess contract.
const (
	CLIKindClaude = "claude_cli"
	CLIKindACP    = "acp"
)

// DefaultOllamaModel is the model a native Ollama provider reports when neither
// the row nor the brand table names one.
const DefaultOllamaModel = "llama3.3"

// Brand is the data half of dispatch: the vendor-specific base URL, default
// model, identity headers, wire/auth declaration and construction quirks that
// used to be `if base == ""` blocks and switch cases in cmd. Brand keys are the
// legacy provider_type strings (store.ProviderX); a brand that is not listed
// here still works — it simply gets no vendor defaults, which is exactly what
// the old `default:` branch did.
type Brand struct {
	// ProviderType is the legacy provider_type / brand id (matches store.ProviderX).
	ProviderType string
	// API is the wire protocol this brand speaks.
	API API
	// BaseURL is the vendor default api_base, applied only when the row/config
	// does not set one. Empty means "the transport's own default".
	BaseURL string
	// Model is the vendor default model, applied only when none is declared.
	Model string
	// ExtraHeaders are static headers the vendor requires on every request.
	ExtraHeaders map[string]string
	// SiteURL/SiteTitle are identification headers (OpenRouter rankings).
	SiteURL   string
	SiteTitle string
	// SessionHeader is the request header a gateway wants filled with a stable
	// per-conversation id (OpenCode Go: x-opencode-session, used for routing and
	// prompt-cache locality). The value is derived from the run's session key.
	SessionHeader string
	// Reflect rules for passing provider_type into the transport.
	Reflect Reflect
	// ThinkingFromSettings says whether llm_providers.settings.thinking_enabled
	// reaches the transport. Brands built by a dedicated branch never applied it
	// on the DB path (the row's settings were ignored), so they must not start.
	ThinkingFromSettings bool
	// Constructor selects a wrapper transport (zero = plain OpenAI-compatible).
	Constructor string
	// CLIKind selects the subprocess transport for wire API cli-delegated.
	CLIKind string
}

// aimlapiPartnerHeaders are the attribution headers the AIMLAPI vendor requires
// on every inference request. NewAIMLAPIProvider also applies them; they are
// listed here so BrandDefaults reports the brand's complete header contract.
func aimlapiPartnerHeaders() map[string]string {
	return map[string]string{
		"X-AIMLAPI-Partner-ID":          "nextlevelbuilder",
		"X-AIMLAPI-Integration-Repo":    "nextlevelbuilder/goclaw",
		"X-AIMLAPI-Integration-Version": "1.0.0",
	}
}

// brands is the brand catalog. Keys are store.ProviderX values; cmd passes the
// same strings, so a new provider of an existing brand is a row insert.
var brands = map[string]Brand{
	// --- Anthropic Messages API -------------------------------------------------
	"anthropic_native": {
		ProviderType: "anthropic_native",
		API:          AnthropicMessages,
	},

	// --- OpenAI Chat Completions, first-party -----------------------------------
	"openai": {
		ProviderType:         "openai",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.openai.com/v1",
		Model:                "gpt-4o",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"openai_compat": {
		ProviderType:         "openai_compat",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.openai.com/v1",
		Model:                "gpt-4o",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},

	// --- OpenAI-compatible gateways ---------------------------------------------
	"openrouter": {
		ProviderType:         "openrouter",
		API:                  OpenAICompletions,
		BaseURL:              "https://openrouter.ai/api/v1",
		Model:                "anthropic/claude-sonnet-4-5-20250929",
		SiteURL:              "https://goclaw.sh",
		SiteTitle:            "GoClaw",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"groq": {
		ProviderType:         "groq",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.groq.com/openai/v1",
		Model:                "llama-3.3-70b-versatile",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"deepseek": {
		ProviderType:         "deepseek",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.deepseek.com/v1",
		Model:                "deepseek-chat",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	// OpenCode's gateways (https://opencode.ai/docs/zen/, /docs/go/). Plain
	// OpenAI-compatible endpoints authenticated with one OpenCode API key; "zen" is
	// the pay-as-you-go catalogue, "opencode_go" the subscription ("Go") gateway.
	//
	// Go's published client contract (https://opencode.ai/docs/go/#where-can-i-use-it)
	// asks clients to identify themselves with their own user agent — not a generic
	// HTTP-library name — and to send a stable conversation id in x-opencode-session
	// "so we can optimize routing and prompt caching".
	"opencode": {
		ProviderType:         "opencode",
		API:                  OpenAICompletions,
		BaseURL:              "https://opencode.ai/zen/v1",
		Model:                "deepseek-v4.1-flash",
		ExtraHeaders:         map[string]string{"User-Agent": "goclaw"},
		SessionHeader:        "x-opencode-session",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"opencode_go": {
		ProviderType:         "opencode_go",
		API:                  OpenAICompletions,
		BaseURL:              "https://opencode.ai/zen/go/v1",
		Model:                "deepseek-v4.1-flash",
		ExtraHeaders:         map[string]string{"User-Agent": "goclaw"},
		SessionHeader:        "x-opencode-session",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"mistral": {
		ProviderType:         "mistral",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.mistral.ai/v1",
		Model:                "mistral-large-latest",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"xai": {
		ProviderType:         "xai",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.x.ai/v1",
		Model:                "grok-3-mini",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"minimax_native": {
		ProviderType:         "minimax_native",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.minimax.io/v1",
		Model:                "MiniMax-M3",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"cohere": {
		ProviderType:         "cohere",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.cohere.ai/compatibility/v1",
		Model:                "command-a",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"perplexity": {
		ProviderType:         "perplexity",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.perplexity.ai",
		Model:                "sonar-pro",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"yescale": {
		ProviderType:         "yescale",
		API:                  OpenAICompletions,
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"novita": {
		ProviderType: "novita",
		API:          OpenAICompletions,
		BaseURL:      "https://api.novita.ai/openai",
		Model:        "moonshotai/kimi-k2.5",
	},
	"atlascloud": {
		ProviderType:         "atlascloud",
		API:                  OpenAICompletions,
		BaseURL:              "https://api.atlascloud.ai/v1",
		Model:                "qwen/qwen3.5-flash",
		Reflect:              ReflectConfigAndDB,
		ThinkingFromSettings: true,
	},
	"aimlapi": {
		ProviderType: "aimlapi",
		API:          OpenAICompletions,
		BaseURL:      "https://api.aimlapi.com/v1",
		Model:        "openai/gpt-5-chat",
		ExtraHeaders: aimlapiPartnerHeaders(),
		Reflect:      ReflectDBOnly,
		Constructor:  constructorAIMLAPI,
	},
	"kimi_coding": {
		ProviderType: "kimi_coding",
		API:          OpenAICompletions,
		BaseURL:      "https://api.kimi.com/coding/v1",
		Model:        "kimi-k2-turbo-preview",
		// The upstream rejects requests without this exact identity header.
		ExtraHeaders: map[string]string{"User-Agent": "claude-code/0.1.0"},
		Reflect:      ReflectDBOnly,
	},

	// --- Alibaba DashScope / Bailian --------------------------------------------
	"dashscope": {
		ProviderType: "dashscope",
		API:          OpenAICompletions,
		Constructor:  constructorDashScope,
	},
	"bailian": {
		ProviderType: "bailian",
		API:          OpenAICompletions,
		BaseURL:      "https://coding-intl.dashscope.aliyuncs.com/v1",
		Model:        "qwen3.5-plus",
		Reflect:      ReflectConfigAndDB,
	},

	// --- Z.AI -------------------------------------------------------------------
	"zai": {
		ProviderType: "zai",
		API:          OpenAICompletions,
		BaseURL:      "https://api.z.ai/api/paas/v4",
		Model:        "glm-5.2",
	},
	"zai_coding": {
		ProviderType: "zai_coding",
		API:          OpenAICompletions,
		BaseURL:      "https://api.z.ai/api/coding/paas/v4",
		Model:        "glm-5.2",
	},

	// --- BytePlus ModelArk ------------------------------------------------------
	"byteplus": {
		ProviderType: "byteplus",
		API:          OpenAICompletions,
		BaseURL:      "https://ark.ap-southeast.bytepluses.com/api/v3",
		Model:        "seed-2-0-lite-260228",
		Reflect:      ReflectConfigAndDB,
	},
	"byteplus_coding": {
		ProviderType: "byteplus_coding",
		API:          OpenAICompletions,
		BaseURL:      "https://ark.ap-southeast.bytepluses.com/api/coding/v3",
		Model:        "seed-2-0-lite-260228",
		Reflect:      ReflectConfigAndDB,
	},

	// --- Google -----------------------------------------------------------------
	"gemini_native": {
		ProviderType:         "gemini_native",
		API:                  GoogleGenerativeAI,
		BaseURL:              "https://generativelanguage.googleapis.com/v1beta/openai",
		Model:                "gemini-2.0-flash",
		Reflect:              ReflectDBOnly,
		ThinkingFromSettings: true,
	},
	"vertex": {
		ProviderType: "vertex",
		API:          GoogleVertex,
	},

	// --- Ollama -----------------------------------------------------------------
	"ollama": {
		ProviderType:         "ollama",
		API:                  OllamaNative,
		Model:                DefaultOllamaModel,
		ThinkingFromSettings: true,
	},
	"ollama_cloud": {
		ProviderType:         "ollama_cloud",
		API:                  OllamaNative,
		BaseURL:              "https://ollama.com",
		Model:                DefaultOllamaModel,
		ThinkingFromSettings: true,
	},

	// --- Subprocess transports --------------------------------------------------
	"chatgpt_oauth": {
		ProviderType: "chatgpt_oauth",
		API:          OpenAICodexResponses,
	},
	"claude_cli": {
		ProviderType: "claude_cli",
		API:          CLIDelegated,
		CLIKind:      CLIKindClaude,
	},
	"acp": {
		ProviderType: "acp",
		API:          CLIDelegated,
		CLIKind:      CLIKindACP,
	},
}

// BrandFor returns the catalog entry for a legacy provider_type. The second
// result is false for custom/unknown brands, which keep working with no vendor
// defaults (the old `default:` branch).
func BrandFor(providerType string) (Brand, bool) {
	b, ok := brands[providerType]
	return b, ok
}

// BrandOrDefault is BrandFor with the "unknown brand" case materialised: only
// ProviderType is set (DB rows pass their provider_type through, as the old
// default branch did) and no vendor defaults apply.
func BrandOrDefault(providerType string) Brand {
	if b, ok := brands[providerType]; ok {
		return b
	}
	return Brand{ProviderType: providerType, Reflect: ReflectDBOnly, ThinkingFromSettings: true}
}

// APIForBrand returns the wire protocol a known brand declares.
func APIForBrand(providerType string) (API, bool) {
	b, ok := brands[providerType]
	if !ok || b.API == "" {
		return "", false
	}
	return b.API, true
}

// BrandDefaults returns the vendor defaults for a brand: its base URL, default
// model, and required identity headers. It is the data replacement for the old
// openAIProviderDefaults switch plus the scattered `if base == ""` blocks.
// Unknown brands return an error so callers cannot silently invent defaults.
func BrandDefaults(providerType string) (baseURL, defaultModel string, extraHeaders map[string]string, err error) {
	b, ok := brands[providerType]
	if !ok {
		return "", "", nil, fmt.Errorf("wire: unknown provider brand %q", providerType)
	}
	return b.BaseURL, b.Model, cloneHeaders(b.ExtraHeaders), nil
}

// BrandProviderTypes returns every catalogued brand id in deterministic order.
func BrandProviderTypes() []string {
	out := make([]string, 0, len(brands))
	for _, b := range brands {
		out = append(out, b.ProviderType)
	}
	sort.Strings(out)
	return out
}

// cloneHeaders copies a header map so callers cannot mutate the catalog.
func cloneHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

// TimeoutFromSettings extracts the operator-declared per-provider request
// timeout from llm_providers.settings ({"timeout_sec": 5}).
//
// A non-positive or absent value returns 0, meaning "no per-provider deadline":
// the transport's stage timeouts (300s response-header bound) still apply. When
// set, the value bounds a whole Chat/ChatStream call for that provider, so an
// operator can cap a slow or unreachable upstream without a global change.
func TimeoutFromSettings(settings json.RawMessage) time.Duration {
	if len(settings) == 0 {
		return 0
	}
	var s struct {
		TimeoutSec int `json:"timeout_sec"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return 0
	}
	if s.TimeoutSec <= 0 {
		return 0
	}
	return time.Duration(s.TimeoutSec) * time.Second
}

// MaxInFlightFromSettings extracts the operator-declared per-provider
// concurrency cap from llm_providers.settings ({"max_in_flight": 2}).
//
// A non-positive or absent value returns 0, meaning "unbounded": the
// provider's ConcurrencyGate stays nil and Chat/ChatStream run with no
// per-provider concurrency limit (existing behavior, unchanged for every
// provider that does not declare this key).
func MaxInFlightFromSettings(settings json.RawMessage) int {
	if len(settings) == 0 {
		return 0
	}
	var s struct {
		MaxInFlight int `json:"max_in_flight"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return 0
	}
	if s.MaxInFlight <= 0 {
		return 0
	}
	return s.MaxInFlight
}
