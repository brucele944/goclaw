package compat

import "strings"

// Endpoint families GoClaw can recognise. A family is what the endpoint *is*,
// not what it is called: inference never reads the provider name.
const (
	FamilyOpenAINative = "openai-native"
	FamilyOpenRouter   = "openrouter"
	FamilyFireworks    = "fireworks"
	FamilyTogether     = "together"
	FamilyDashScope    = "dashscope"
	FamilyOllama       = "ollama"
)

// familyRule infers an endpoint family from declared, machine-readable inputs.
// Rules are ordered; the first match wins. wireAPIs is a restrictive list (empty
// = any registered wire API).
type familyRule struct {
	family       string
	wireAPIs     []string
	typeContains []string
	baseContains []string
}

var familyRules = []familyRule{
	{
		// Ollama local (":11434"), self-hosted/proxied instances referenced by a
		// hostname, and rows whose declared provider_type is an Ollama brand.
		family:       FamilyOllama,
		wireAPIs:     []string{"openai-completions"},
		typeContains: []string{"ollama"},
		baseContains: []string{":11434", "ollama"},
	},
	{
		family:       FamilyFireworks,
		wireAPIs:     []string{"openai-completions"},
		baseContains: []string{"fireworks.ai"},
	},
	{
		// provider_type is checked as well so reverse-proxied Together
		// deployments (not reachable by URL) are still recognised.
		family:       FamilyTogether,
		wireAPIs:     []string{"openai-completions"},
		typeContains: []string{"together"},
		baseContains: []string{"together.xyz", "together.ai"},
	},
	{
		// "bailian" is DashScope's Alibaba Cloud product name and appears as a
		// provider_type on live rows.
		family:       FamilyDashScope,
		wireAPIs:     []string{"openai-completions"},
		typeContains: []string{"dashscope", "bailian"},
		baseContains: []string{"dashscope"},
	},
	{
		family:       FamilyOpenRouter,
		wireAPIs:     []string{"openai-completions"},
		typeContains: []string{"openrouter"},
		baseContains: []string{"openrouter.ai"},
	},
	{
		// Last: api.openai.com is unambiguous and must not shadow the others.
		family:       FamilyOpenAINative,
		wireAPIs:     []string{"openai-completions", "openai-codex-responses"},
		baseContains: []string{"api.openai.com"},
	},
}

// InferFamily resolves the endpoint family from declared inputs. The provider
// name is not an input by design.
func InferFamily(wireAPI, providerType, apiBase, declared string) string {
	if d := strings.ToLower(strings.TrimSpace(declared)); d != "" {
		return d
	}
	for _, rule := range familyRules {
		if rule.matches(wireAPI, providerType, apiBase) {
			return rule.family
		}
	}
	return ""
}

func (r familyRule) matches(wireAPI, providerType, apiBase string) bool {
	if len(r.wireAPIs) > 0 {
		ok := false
		for _, w := range r.wireAPIs {
			if strings.EqualFold(w, wireAPI) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	// The declared type and the base URL are alternatives (a reverse proxy hides
	// the upstream host, and a self-hosted instance may declare no vendor type).
	if len(r.typeContains) == 0 && len(r.baseContains) == 0 {
		return true
	}
	pt := strings.ToLower(strings.TrimSpace(providerType))
	for _, want := range r.typeContains {
		if want != "" && strings.Contains(pt, want) {
			return true
		}
	}
	base := strings.ToLower(apiBase)
	for _, want := range r.baseContains {
		if want != "" && strings.Contains(base, want) {
			return true
		}
	}
	return false
}

// IsNativeOpenAIEndpoint reports whether the base URL is first-party OpenAI
// infrastructure (accepts the "developer" message role and prompt-cache params).
func IsNativeOpenAIEndpoint(apiBase string) bool {
	return InferFamily("openai-completions", "", apiBase, "") == FamilyOpenAINative
}

// MaxTokensFieldFor returns the body key for the completion budget for a model
// id. OpenAI's reasoning families replaced max_tokens with
// max_completion_tokens; every other family (and every proxy) keeps the legacy
// key. Returns "" for an empty model so callers can keep a declared override.
func MaxTokensFieldFor(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	fam := strings.ToLower(ModelFamily(model))
	for _, prefix := range []string{"gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(fam, prefix) {
			return "max_completion_tokens"
		}
	}
	return "max_tokens"
}

// ModelFamily strips a provider prefix (for example "openai/o3-mini") so model
// gates apply to the model rather than the transport wrapper.
func ModelFamily(model string) string {
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx < len(model)-1 {
		return model[idx+1:]
	}
	return model
}
