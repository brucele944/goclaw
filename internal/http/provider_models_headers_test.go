package http

// The /models discovery call is built outside the provider transport, so it needs
// its own coverage: a brand that declares identity or session headers must send
// them here too (OpenCode Go answers without x-opencode-session by rejecting the
// request as unroutable — the error a user sees when picking a model).

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestOpenAIModelsExtraHeaders_OpenCodeIdentity(t *testing.T) {
	for _, providerType := range []string{store.ProviderOpenCode, store.ProviderOpenCodeGo} {
		headers := openAIModelsExtraHeaders(providerType)
		if got := headers["User-Agent"]; got != "goclaw" {
			t.Errorf("%s User-Agent = %q, want goclaw", providerType, got)
		}
		if got := headers["x-opencode-session"]; got == "" {
			t.Errorf("%s: x-opencode-session missing from the listing call", providerType)
		}
	}
}

func TestOpenAIModelsExtraHeaders_UnchangedForOthers(t *testing.T) {
	if got := openAIModelsExtraHeaders(store.ProviderKimiCoding)["User-Agent"]; got != store.KimiCodingRequiredUserAgent {
		t.Errorf("kimi_coding User-Agent = %q, want %q", got, store.KimiCodingRequiredUserAgent)
	}
	if got := openAIModelsExtraHeaders(store.ProviderOpenAICompat); got != nil {
		t.Errorf("openai_compat headers = %v, want none", got)
	}
}
