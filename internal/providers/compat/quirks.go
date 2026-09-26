package compat

import (
	"encoding/json"
	"strings"
)

// Quirk is one declared compatibility rule, the in-memory mirror of a
// provider_quirks row. The bundled table below is the source of truth for
// endpoint-family behaviour; the persisted rows exist so operators can inspect,
// override, or disable a rule without rebuilding.
type Quirk struct {
	// WireAPI scopes the row to a declared wire protocol ("" = any).
	WireAPI string
	// EndpointFamily scopes the row to an endpoint family ("" = any).
	EndpointFamily string
	// ModelPattern is a glob ("*" wildcard) matched against the model id
	// ("" = any model).
	ModelPattern string
	// Compat is the resolved compat fragment.
	Compat json.RawMessage
	// Note explains the rule for operators.
	Note string
	// Source is "bundled" or "operator".
	Source string
	// Enabled false suppresses the bundled rows it matches.
	Enabled bool
}

// matches reports whether the quirk applies to this wire/family/model.
func (q Quirk) matches(wireAPI, family, model string) bool {
	if q.WireAPI != "" && !strings.EqualFold(q.WireAPI, wireAPI) {
		return false
	}
	if q.EndpointFamily != "" && !strings.EqualFold(q.EndpointFamily, family) {
		return false
	}
	if q.ModelPattern != "" && !patternMatches(q.ModelPattern, model) {
		return false
	}
	return true
}

// patternMatches applies a simple case-insensitive glob: "*" matches any run of
// characters. No regexp, no allocation growth.
func patternMatches(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	p := strings.ToLower(pattern)
	v := strings.ToLower(value)
	parts := strings.Split(p, "*")
	if len(parts) == 1 {
		return p == v
	}
	if !strings.HasPrefix(v, parts[0]) {
		return false
	}
	v = v[len(parts[0]):]
	last := parts[len(parts)-1]
	middle := parts[1 : len(parts)-1]
	for _, part := range middle {
		idx := strings.Index(v, part)
		if idx < 0 {
			return false
		}
		v = v[idx+len(part):]
	}
	if last == "" {
		return true
	}
	return strings.HasSuffix(v, last)
}

// bundledSeeds is the code-generated quirk table. Each row was derived from the
// sniffing helper it replaces, so behaviour is byte-identical at cutover:
//
//	isOllamaEndpoint         → family "ollama"      (type/base URL only; the
//	                            provider-name clause is deliberately gone)
//	isFireworksEndpoint      → family "fireworks"
//	isTogetherEndpoint       → family "together"    (type/base URL only)
//	isDashScopeAPIBase       → family "dashscope"
//	isOpenAINativeEndpoint   → family "openai-native"
func bundledSeeds() []Quirk {
	return []Quirk{
		{
			WireAPI:        "openai-completions",
			EndpointFamily: FamilyOpenAINative,
			Compat:         json.RawMessage(`{"supports_developer_role":true,"supports_store":true}`),
			Note:           "First-party OpenAI: developer role and prompt-cache params accepted, store supported.",
			Source:         "bundled",
			Enabled:        true,
		},
		{
			WireAPI:        "openai-completions",
			EndpointFamily: FamilyOllama,
			Compat: json.RawMessage(`{"native_chat_path":"/api/chat","ollama_options":true,` +
				`"ollama_think":true,"supports_thinking":false}`),
			Note:    "Ollama: native /api/chat honours options.num_ctx; thinking is suppressed unless requested.",
			Source:  "bundled",
			Enabled: true,
		},
		{
			WireAPI:        "openai-completions",
			EndpointFamily: FamilyTogether,
			Compat:         json.RawMessage(`{"stream_options":false}`),
			Note:           "Together rejects stream_options with HTTP 400.",
			Source:         "bundled",
			Enabled:        true,
		},
		{
			WireAPI:        "openai-completions",
			EndpointFamily: FamilyFireworks,
			Compat:         json.RawMessage(`{"clamp_max_tokens":4096}`),
			Note:           "Fireworks requires stream=true for max_tokens above 4096; non-streaming requests are clamped.",
			Source:         "bundled",
			Enabled:        true,
		},
		{
			WireAPI:        "openai-completions",
			EndpointFamily: FamilyDashScope,
			Compat: json.RawMessage(`{"system_cache_control":true,"tool_prefix_cache":true,` +
				`"dashscope_passthrough":true}`),
			Note:    "DashScope/Bailian: prompt-cache blocks and the enable_thinking/thinking_budget passthrough keys.",
			Source:  "bundled",
			Enabled: true,
		},
	}
}

// Bundled returns the bundled quirk seeds. Callers may persist them (see
// catalog) or resolve against them directly; the rows are stable and idempotent.
func Bundled() []Quirk { return bundledSeeds() }
