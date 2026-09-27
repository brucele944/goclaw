package providers

import "testing"

func TestSplitModelRef(t *testing.T) {
	known := func(name string) bool { return name == "groq" || name == "openrouter" }

	tests := []struct {
		name        string
		ref         string
		provider    string
		model       string
		hasProvider bool
		knownFn     func(string) bool
	}{
		{name: "composite with known provider", ref: "groq/llama-3.3-70b", provider: "groq", model: "llama-3.3-70b", hasProvider: true, knownFn: known},
		{name: "composite keeps multi-segment model id", ref: "openrouter/openai/gpt-5.5", provider: "openrouter", model: "openai/gpt-5.5", hasProvider: true, knownFn: known},
		{name: "unknown prefix stays a bare model id", ref: "openai/gpt-5.5", model: "openai/gpt-5.5", knownFn: known},
		{name: "bare model id", ref: "llama-3.3-70b", model: "llama-3.3-70b", knownFn: known},
		{name: "no resolver means bare", ref: "groq/llama-3.3-70b", model: "groq/llama-3.3-70b"},
		{name: "empty", ref: "   ", knownFn: known},
		{name: "trailing separator is not a reference", ref: "groq/", model: "groq/", knownFn: known},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, model, has := SplitModelRef(tc.ref, tc.knownFn)
			if provider != tc.provider || model != tc.model || has != tc.hasProvider {
				t.Fatalf("SplitModelRef(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.ref, provider, model, has, tc.provider, tc.model, tc.hasProvider)
			}
		})
	}
}
