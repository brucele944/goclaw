package http

// The create/update handler registers providers through its own per-type switch
// (the boot path uses wire.Build instead). A type without a dedicated branch has
// to pick up the wire catalog, otherwise a freshly created provider behaves
// differently from the same row after a restart — an OpenCode row ended up with
// the OpenAI base URL, a generic User-Agent and no x-opencode-session, which the
// gateway rejects as unroutable (2026-09-29).

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestOpenAIBrandDefaults_OpenCodeUsesCatalog(t *testing.T) {
	for _, tc := range []struct {
		providerType string
		baseURL      string
	}{
		{store.ProviderOpenCodeGo, "https://opencode.ai/zen/go/v1"},
		{store.ProviderOpenCode, "https://opencode.ai/zen/v1"},
	} {
		base, model, brand, ok := openAIBrandDefaults(tc.providerType, "")
		if !ok {
			t.Fatalf("%s: no brand found in the catalog", tc.providerType)
		}
		if base != tc.baseURL {
			t.Errorf("%s base = %q, want %q (empty row api_base must not fall back to OpenAI)",
				tc.providerType, base, tc.baseURL)
		}
		if model != "deepseek-v4.1-flash" {
			t.Errorf("%s model = %q, want the brand default", tc.providerType, model)
		}
		if brand.SessionHeader != "x-opencode-session" {
			t.Errorf("%s SessionHeader = %q, want x-opencode-session", tc.providerType, brand.SessionHeader)
		}
		if brand.ExtraHeaders["User-Agent"] != "goclaw" {
			t.Errorf("%s User-Agent = %q, want goclaw", tc.providerType, brand.ExtraHeaders["User-Agent"])
		}

		// The constructed provider must carry them too: a provider with the OpenAI
		// base URL would send the OpenCode key to api.openai.com.
		prov := providers.NewOpenAIProvider("t", "k", base, model).
			WithExtraHeaders(brand.ExtraHeaders).
			WithSessionHeader(brand.SessionHeader)
		if got := prov.APIBase(); got != tc.baseURL {
			t.Errorf("APIBase() = %q, want %q", got, tc.baseURL)
		}
		if got := prov.SessionHeader(); got != "x-opencode-session" {
			t.Errorf("SessionHeader() = %q, want x-opencode-session", got)
		}
	}
}

// An explicit api_base still wins over the brand default, and a type with no
// dedicated branch but also no catalog entry keeps the legacy behaviour.
func TestOpenAIBrandDefaults_PreservesExistingBehaviour(t *testing.T) {
	base, _, _, ok := openAIBrandDefaults(store.ProviderOpenCodeGo, "https://proxy.internal/v1")
	if !ok || base != "https://proxy.internal/v1" {
		t.Errorf("base = %q (ok=%v), want the row's api_base", base, ok)
	}

	base, model, _, ok := openAIBrandDefaults("something_unlisted", "https://x/v1")
	if ok || base != "https://x/v1" || model != "" {
		t.Errorf("unlisted type = (%q, %q, ok=%v), want the legacy passthrough", base, model, ok)
	}

	if base, model, _, _ := openAIBrandDefaults(store.ProviderMiniMax, ""); base == "" || model != store.MiniMaxDefaultModel {
		t.Errorf("minimax = (%q, %q), want its legacy defaults", base, model)
	}
}
