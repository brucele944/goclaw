package discovery

import "github.com/nextlevelbuilder/goclaw/internal/providers"

// Bundled snapshots: the model lists GoClaw ships for first boot and offline
// use, one per provider type that has no (or no reliable) listing endpoint.
//
// This is the single source of truth for the catalogues that used to be Go
// literals in internal/http/provider_models_catalog.go plus the eleven
// ModelRegistry seeds. They are seeded into llm_models rows with
// source='bundled' (internal/providers/catalog) and served as the `static`
// discovery so the row set and the served set can never disagree.
//
// Only what the code already knew is filled in: context window, output cap,
// tokenizer and the reasoning/vision flags of the ModelRegistry seeds. Anything
// unknown stays unset so the column stays NULL instead of guessing.

// bundledModel is one snapshot entry. Zero values mean "unknown", except in the
// capability flags, which are only meaningful when capsDeclared is true.
type bundledModel struct {
	id      string
	label   string
	context int
	maxOut  int
	// capsDeclared says the reasoning/vision flags below are known values
	// rather than "not declared" (the hardcoded catalogues only know id+label).
	capsDeclared bool
	reasoning    bool
	vision       bool
	// Request-path capability flags (provider rework, phase 5). nil = the entry
	// declares nothing for that key, so the request path keeps the provider's own
	// Capabilities() value. Only the entries that actually differ from their
	// transport's default set them: DashScope cannot stream while tools are
	// in-flight, everything else here seeds today's streaming behaviour
	// explicitly so a capability consumer never has to guess.
	toolCalling     *bool
	streamWithTools *bool
	cacheControl    *bool
	tokenizer       string
}

// declaredCapabilities returns the capability keys this entry declares, or nil
// when it declares none (zero-value entries from the hardcoded catalogues).
func (e bundledModel) declaredCapabilities() map[string]bool {
	var caps map[string]bool
	if e.capsDeclared {
		caps = map[string]bool{"reasoning": e.reasoning, "vision": e.vision}
	}
	set := func(key string, v *bool) {
		if v == nil {
			return
		}
		if caps == nil {
			caps = make(map[string]bool, 5)
		}
		caps[key] = *v
	}
	set("tool_calling", e.toolCalling)
	set("stream_with_tools", e.streamWithTools)
	set("cache_control", e.cacheControl)
	return caps
}

// bundledCatalogs maps a provider type (store.ProviderX) to its snapshot, in
// the order the pre-catalog code returned.
var bundledCatalogs = buildBundledCatalogs()

func buildBundledCatalogs() map[string][]bundledModel {
	zai := []bundledModel{
		{id: "glm-5.2", label: "GLM 5.2"},
		{id: "glm-5.1", label: "GLM 5.1"},
		{id: "glm-5-turbo", label: "GLM 5 Turbo"},
		{id: "glm-5", label: "GLM 5"},
		{id: "glm-4.7", label: "GLM 4.7"},
		{id: "glm-4.7-flash", label: "GLM 4.7 Flash"},
		{id: "glm-4.7-flashx", label: "GLM 4.7 FlashX"},
		{id: "glm-4.6", label: "GLM 4.6"},
		{id: "glm-4.5", label: "GLM 4.5"},
		{id: "glm-4.5-air", label: "GLM 4.5 Air"},
		{id: "glm-4.5-x", label: "GLM 4.5 X"},
		{id: "glm-4.5-airx", label: "GLM 4.5 AirX"},
		{id: "glm-4.5-flash", label: "GLM 4.5 Flash"},
		{id: "glm-4-32b-0414-128k", label: "GLM 4 32B 0414 128K"},
	}
	return map[string][]bundledModel{
		// --- Anthropic Messages (ModelRegistry seeds) ---------------------------
		"anthropic_native": {
			// TokenizerID "cl100k_base" is an approximation — Claude uses a
			// proprietary tokenizer; it is used for rough token estimation only.
			{id: "claude-opus-4-6", context: 200_000, maxOut: 32_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), cacheControl: new(true), tokenizer: "cl100k_base"},
			{id: "claude-sonnet-4-6", context: 200_000, maxOut: 16_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), cacheControl: new(true), tokenizer: "cl100k_base"},
			{id: "claude-haiku-4-5-20251001", context: 200_000, maxOut: 8_192, capsDeclared: true, reasoning: false, vision: true, toolCalling: new(true), streamWithTools: new(true), cacheControl: new(true), tokenizer: "cl100k_base"},
		},

		// --- OpenAI first-party (ModelRegistry seeds) ---------------------------
		"openai": {
			{id: "gpt-5.6-sol", context: 1_050_000, maxOut: 128_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "gpt-5.6-terra", context: 1_050_000, maxOut: 128_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "gpt-5.5", context: 1_050_000, maxOut: 128_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "gpt-5.4", context: 1_000_000, maxOut: 100_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "gpt-5.2", context: 256_000, maxOut: 64_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "gpt-4o", context: 128_000, maxOut: 16_384, capsDeclared: true, reasoning: false, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "o3", context: 200_000, maxOut: 100_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
			{id: "o4-mini", context: 200_000, maxOut: 100_000, capsDeclared: true, reasoning: true, vision: true, toolCalling: new(true), streamWithTools: new(true), tokenizer: "o200k_base"},
		},

		// --- ChatGPT OAuth (Codex) ---------------------------------------------
		"chatgpt_oauth": {
			{id: "gpt-5.6-sol", label: "GPT-5.6 Sol"},
			{id: "gpt-5.6-terra", label: "GPT-5.6 Terra"},
			{id: "gpt-5.5", label: "GPT-5.5"},
			{id: "gpt-5.4", label: "GPT-5.4"},
			{id: "gpt-5.4-mini", label: "GPT-5.4 Mini"},
			{id: "gpt-5.3-codex", label: "GPT-5.3 Codex"},
			{id: "gpt-5.3-codex-spark", label: "GPT-5.3 Codex Spark"},
			{id: "gpt-5.2-codex", label: "GPT-5.2 Codex"},
			{id: "gpt-5.2", label: "GPT-5.2"},
			{id: "gpt-5.1-codex", label: "GPT-5.1 Codex"},
			{id: "gpt-5.1-codex-max", label: "GPT-5.1 Codex Max"},
			{id: "gpt-5.1-codex-mini", label: "GPT-5.1 Codex Mini"},
			{id: "gpt-5.1", label: "GPT-5.1"},
		},

		// --- Claude CLI (accepted aliases, not a listing API) -------------------
		"claude_cli": {
			{id: "claude-opus-4-6", label: "Opus 4.6"},
			{id: "claude-sonnet-4-6", label: "Sonnet 4.6"},
			{id: "claude-haiku-4-5-20251001", label: "Haiku 4.5 (20251001)"},
			{id: "sonnet[1m]", label: "Sonnet (1M context)"},
			{id: "sonnet", label: "Sonnet"},
			{id: "opus", label: "Opus"},
			{id: "haiku", label: "Haiku"},
		},

		// --- ACP coding agents --------------------------------------------------
		"acp": {
			{id: "claude", label: "Claude"},
			{id: "codex", label: "Codex"},
			{id: "gemini", label: "Gemini"},
		},

		// --- Bailian Coding (no /v1/models endpoint) ----------------------------
		"bailian": {
			// qwen3.7-plus: Text Generation + Deep Thinking + Visual Understanding.
			{id: "qwen3.7-plus", label: "Qwen 3.7 Plus"},
			{id: "qwen3.6-plus", label: "Qwen 3.6 Plus"},
			{id: "qwen3.5-plus", label: "Qwen 3.5 Plus"},
			{id: "kimi-k2.5", label: "Kimi K2.5"},
			{id: "GLM-5", label: "GLM-5"},
			{id: "MiniMax-M2.5", label: "MiniMax M2.5"},
			{id: "qwen3-max-2026-01-23", label: "Qwen 3 Max (2026-01-23)"},
			{id: "qwen3-coder-next", label: "Qwen 3 Coder Next"},
			{id: "qwen3-coder-plus", label: "Qwen 3 Coder Plus"},
			{id: "glm-4.7", label: "GLM 4.7"},
		},

		// --- DashScope (no standard /v1/models endpoint) ------------------------
		// DashScope rejects a streaming request that carries tools, so every chat
		// model declares stream_with_tools:false — the request path forces the
		// non-stream transport from this declaration instead of sniffing the
		// provider name/type. The image/video entries are not chat models and
		// declare nothing.
		"dashscope": {
			// Qwen3.6 series — Agentic Coding + 1M context
			{id: "qwen3.6-plus", label: "Qwen 3.6 Plus", toolCalling: new(true), streamWithTools: new(false)},
			// Qwen3.5 series — Text Generation + Deep Thinking + Visual Understanding
			{id: "qwen3.5-plus", label: "Qwen 3.5 Plus", toolCalling: new(true), streamWithTools: new(false)},
			{id: "qwen3.5-flash", label: "Qwen 3.5 Flash", toolCalling: new(true), streamWithTools: new(false)},
			{id: "qwen3.5-turbo", label: "Qwen 3.5 Turbo", toolCalling: new(true), streamWithTools: new(false)},
			// Qwen3 hosted series — Text + Thinking
			{id: "qwen3-max", label: "Qwen 3 Max", toolCalling: new(true), streamWithTools: new(false)},
			{id: "qwen3-plus", label: "Qwen 3 Plus", toolCalling: new(true), streamWithTools: new(false)},
			{id: "qwen3-turbo", label: "Qwen 3 Turbo", toolCalling: new(true), streamWithTools: new(false)},
			// Image generation
			{id: "wan2.6-image", label: "Wan 2.6 Image"},
			{id: "wan2.1-image", label: "Wan 2.1 Image"},
			// Video generation
			{id: "wan2.6-video", label: "Wan 2.6 Video"},
		},

		// --- MiniMax (no /v1/models endpoint) -----------------------------------
		"minimax_native": {
			// Chat / text
			{id: "MiniMax-M3", label: "MiniMax M3"},
			{id: "MiniMax-Text-01", label: "MiniMax Text 01"},
			{id: "MiniMax-M1", label: "MiniMax M1"},
			{id: "MiniMax-M2.7", label: "MiniMax M2.7"},
			{id: "MiniMax-M2.7-highspeed", label: "MiniMax M2.7 Highspeed"},
			{id: "MiniMax-M2.5", label: "MiniMax M2.5"},
			{id: "MiniMax-M2.5-highspeed", label: "MiniMax M2.5 Highspeed"},
			{id: "MiniMax-M2.1", label: "MiniMax M2.1"},
			{id: "MiniMax-M2.1-highspeed", label: "MiniMax M2.1 Highspeed"},
			{id: "MiniMax-M2", label: "MiniMax M2"},
			// Image generation
			{id: "image-01", label: "Image 01"},
			// Video generation
			{id: "MiniMax-Hailuo-2.3", label: "Hailuo Video 2.3"},
			{id: "MiniMax-Hailuo-2", label: "Hailuo Video 2"},
			{id: "T2V-01-Director", label: "T2V-01 Director"},
			// Music generation
			{id: "music-2.5+", label: "Music 2.5+"},
			{id: "music-2.5", label: "Music 2.5"},
			// TTS
			{id: "speech-02-hd", label: "Speech 02 HD"},
			{id: "speech-02-turbo", label: "Speech 02 Turbo"},
		},

		// --- Z.AI GLM (coding plan shares the catalogue) ------------------------
		"zai":        zai,
		"zai_coding": zai,

		// --- AIMLAPI curated text catalog (mirrors providers.AIMLAPIChatModels) --
		"aimlapi": aimlapiBundled(),
	}
}

// aimlapiBundled derives the AIMLAPI snapshot from the transport's curated list
// so the picker and the shipped transport can never drift apart.
func aimlapiBundled() []bundledModel {
	ids := providers.AIMLAPIChatModels()
	out := make([]bundledModel, 0, len(ids))
	for _, id := range ids {
		// The AIMLAPI picker labels models by their id (legacy behaviour).
		out = append(out, bundledModel{id: id, label: id})
	}
	return out
}

// Bundled returns the shipped snapshot for a provider type, or nil when the
// provider type has no bundled catalogue (its models come from discovery only).
// The returned slice is freshly built: callers may not mutate the snapshot.
func Bundled(providerType string) []ModelInfo {
	entries, ok := bundledCatalogs[providerType]
	if !ok {
		return nil
	}
	out := make([]ModelInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.modelInfo())
	}
	return out
}

// HasBundled reports whether a provider type ships a snapshot.
func HasBundled(providerType string) bool {
	_, ok := bundledCatalogs[providerType]
	return ok
}

func (e bundledModel) modelInfo() ModelInfo {
	info := ModelInfo{ID: e.id, DisplayName: e.label, Tokenizer: e.tokenizer}
	if e.context > 0 {
		info.ContextWindow = intPtr(e.context)
	}
	if e.maxOut > 0 {
		info.MaxTokens = intPtr(e.maxOut)
	}
	info.Capabilities = e.declaredCapabilities()
	if e.capsDeclared && e.vision {
		info.Modalities = []string{"text", "image"}
	}
	return info
}

// BundledModelCapabilities returns the capability override the shipped snapshot
// declares for one (provider type, model id). ok=false when the provider type
// has no snapshot, the model is not in it, or the entry declares nothing the
// request path consumes — the caller then keeps the provider's own
// Capabilities() unchanged.
//
// This is the request path's default ModelCapabilityLookup: the snapshot is the
// same table the catalogue seeds into llm_models rows, so the row and the served
// capability can never disagree.
func BundledModelCapabilities(providerType, modelID string) (providers.ModelCapabilityOverride, bool) {
	entries, ok := bundledCatalogs[providerType]
	if !ok {
		return providers.ModelCapabilityOverride{}, false
	}
	for _, e := range entries {
		if e.id != modelID {
			continue
		}
		var override providers.ModelCapabilityOverride
		declared := false
		if e.toolCalling != nil {
			override.ToolCalling, declared = e.toolCalling, true
		}
		if e.vision && e.capsDeclared {
			override.Vision, declared = new(true), true
		}
		if e.streamWithTools != nil {
			override.StreamWithTools, declared = e.streamWithTools, true
		}
		if e.cacheControl != nil {
			override.CacheControl, declared = e.cacheControl, true
		}
		if e.context > 0 {
			override.MaxContextWindow, declared = e.context, true
		}
		return override, declared
	}
	return providers.ModelCapabilityOverride{}, false
}

func intPtr(v int) *int { return &v }
