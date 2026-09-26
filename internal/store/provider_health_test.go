package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseProviderFallbackChain(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     []ModelFallbackCandidate
	}{
		{"absent key", `{"timeout_sec":30}`, nil},
		{"empty settings", ``, nil},
		{"malformed settings", `{not json`, nil},
		{"object entries", `{"fallback_chain":[{"provider":"anthropic","model":"claude-sonnet-4"}]}`,
			[]ModelFallbackCandidate{{Provider: "anthropic", Model: "claude-sonnet-4"}}},
		{"compact string entries", `{"fallback_chain":["anthropic/claude-sonnet-4","groq/llama-3.3-70b"]}`,
			[]ModelFallbackCandidate{{Provider: "anthropic", Model: "claude-sonnet-4"}, {Provider: "groq", Model: "llama-3.3-70b"}}},
		{"vendor-prefixed model id keeps its slash", `{"fallback_chain":["openrouter/google/gemini-2.5-pro"]}`,
			[]ModelFallbackCandidate{{Provider: "openrouter", Model: "google/gemini-2.5-pro"}}},
		{"entry missing a field is dropped", `{"fallback_chain":[{"provider":"anthropic"},{"provider":"groq","model":"llama"}]}`,
			[]ModelFallbackCandidate{{Provider: "groq", Model: "llama"}}},
		{"string without a slash is dropped", `{"fallback_chain":["justaname",{"provider":"groq","model":"llama"}]}`,
			[]ModelFallbackCandidate{{Provider: "groq", Model: "llama"}}},
		{"empty chain", `{"fallback_chain":[]}`, nil},
		{"whitespace is trimmed", `{"fallback_chain":[{"provider":" anthropic ","model":" model-x "}]}`,
			[]ModelFallbackCandidate{{Provider: "anthropic", Model: "model-x"}}},
		{"a non-object/non-string entry fails the chain closed", `{"fallback_chain":[42]}`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseProviderFallbackChain(json.RawMessage(tt.settings))
			if len(got) != len(tt.want) {
				t.Fatalf("ParseProviderFallbackChain() = %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("entry %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMergeFallbackCandidates(t *testing.T) {
	agent := []ModelFallbackCandidate{{Provider: "p1", Model: "m1"}, {Provider: "both", Model: "shared"}}
	prov := []ModelFallbackCandidate{{Provider: "both", Model: "shared"}, {Provider: "p2", Model: "m2"}}

	got := MergeFallbackCandidates(agent, prov)
	want := []ModelFallbackCandidate{
		{Provider: "p1", Model: "m1"},
		{Provider: "both", Model: "shared"},
		{Provider: "p2", Model: "m2"},
	}
	if len(got) != len(want) {
		t.Fatalf("MergeFallbackCandidates() = %+v, want %+v (agent chain wins, provider chain appended, dedup)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if got := MergeFallbackCandidates(nil, nil); got != nil {
		t.Errorf("MergeFallbackCandidates(nil, nil) = %+v, want nil", got)
	}
	if got := MergeFallbackCandidates(nil, []ModelFallbackCandidate{{Provider: "p", Model: "m"}}); len(got) != 1 {
		t.Errorf("MergeFallbackCandidates(nil, chain) = %+v, want the provider chain", got)
	}
	// Incomplete pairs never reach the wrapper (they cannot be resolved).
	if got := MergeFallbackCandidates([]ModelFallbackCandidate{{Provider: "p"}}, []ModelFallbackCandidate{{Model: "m"}}); got != nil {
		t.Errorf("MergeFallbackCandidates() = %+v, want nil for incomplete pairs", got)
	}
}

func TestNormalizeErrorClass(t *testing.T) {
	cases := map[string]string{
		"rate_limit":       "rate_limit",
		" RATE_LIMIT ":     "rate_limit",
		"":                 ErrorClassUnknown,
		"auth; DROP TABLE": ErrorClassUnknown,
		"rate limit":       ErrorClassUnknown,
		"model_not_found":  "model_not_found",
		"unknown":          "unknown",
	}
	for in, want := range cases {
		if got := NormalizeErrorClass(in); got != want {
			t.Errorf("NormalizeErrorClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProviderHealthCoolingDown(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Minute)

	health := NewProviderHealth([16]byte{})
	if health.CoolingDown(now) {
		t.Error("a provider that never failed must not report cooling down")
	}
	health.CooldownUntil = &until
	if !health.CoolingDown(now) {
		t.Error("an active deadline must report cooling down")
	}
	if health.CoolingDown(until) {
		t.Error("the deadline itself is not cooling down anymore")
	}
	if health.CoolingDown(until.Add(time.Second)) {
		t.Error("an expired deadline must report available")
	}
	var nilHealth *ProviderHealth
	if nilHealth.CoolingDown(now) {
		t.Error("nil health must report available")
	}
}
