package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// legacyCatalog pins the pre-phase response of GET /v1/providers/{id}/models for
// every provider type that returned a hardcoded list, in order. The bundled
// snapshot must reproduce it row for row: these entries moved out of Go literals
// into llm_models rows, and a single dropped or renamed model silently withdraws
// a working model from the picker.
func legacyCatalog(t *testing.T) map[string][]ModelInfo {
	t.Helper()
	aimlapi := make([]ModelInfo, 0, 4)
	for _, id := range providers.AIMLAPIChatModels() {
		aimlapi = append(aimlapi, ModelInfo{ID: id, DisplayName: id})
	}
	return map[string][]ModelInfo{
		"bailian": {
			{ID: "qwen3.7-plus", DisplayName: "Qwen 3.7 Plus"},
			{ID: "qwen3.6-plus", DisplayName: "Qwen 3.6 Plus"},
			{ID: "qwen3.5-plus", DisplayName: "Qwen 3.5 Plus"},
			{ID: "kimi-k2.5", DisplayName: "Kimi K2.5"},
			{ID: "GLM-5", DisplayName: "GLM-5"},
			{ID: "MiniMax-M2.5", DisplayName: "MiniMax M2.5"},
			{ID: "qwen3-max-2026-01-23", DisplayName: "Qwen 3 Max (2026-01-23)"},
			{ID: "qwen3-coder-next", DisplayName: "Qwen 3 Coder Next"},
			{ID: "qwen3-coder-plus", DisplayName: "Qwen 3 Coder Plus"},
			{ID: "glm-4.7", DisplayName: "GLM 4.7"},
		},
		"dashscope": {
			{ID: "qwen3.6-plus", DisplayName: "Qwen 3.6 Plus"},
			{ID: "qwen3.5-plus", DisplayName: "Qwen 3.5 Plus"},
			{ID: "qwen3.5-flash", DisplayName: "Qwen 3.5 Flash"},
			{ID: "qwen3.5-turbo", DisplayName: "Qwen 3.5 Turbo"},
			{ID: "qwen3-max", DisplayName: "Qwen 3 Max"},
			{ID: "qwen3-plus", DisplayName: "Qwen 3 Plus"},
			{ID: "qwen3-turbo", DisplayName: "Qwen 3 Turbo"},
			{ID: "wan2.6-image", DisplayName: "Wan 2.6 Image"},
			{ID: "wan2.1-image", DisplayName: "Wan 2.1 Image"},
			{ID: "wan2.6-video", DisplayName: "Wan 2.6 Video"},
		},
		"minimax_native": {
			{ID: "MiniMax-M3", DisplayName: "MiniMax M3"},
			{ID: "MiniMax-Text-01", DisplayName: "MiniMax Text 01"},
			{ID: "MiniMax-M1", DisplayName: "MiniMax M1"},
			{ID: "MiniMax-M2.7", DisplayName: "MiniMax M2.7"},
			{ID: "MiniMax-M2.7-highspeed", DisplayName: "MiniMax M2.7 Highspeed"},
			{ID: "MiniMax-M2.5", DisplayName: "MiniMax M2.5"},
			{ID: "MiniMax-M2.5-highspeed", DisplayName: "MiniMax M2.5 Highspeed"},
			{ID: "MiniMax-M2.1", DisplayName: "MiniMax M2.1"},
			{ID: "MiniMax-M2.1-highspeed", DisplayName: "MiniMax M2.1 Highspeed"},
			{ID: "MiniMax-M2", DisplayName: "MiniMax M2"},
			{ID: "image-01", DisplayName: "Image 01"},
			{ID: "MiniMax-Hailuo-2.3", DisplayName: "Hailuo Video 2.3"},
			{ID: "MiniMax-Hailuo-2", DisplayName: "Hailuo Video 2"},
			{ID: "T2V-01-Director", DisplayName: "T2V-01 Director"},
			{ID: "music-2.5+", DisplayName: "Music 2.5+"},
			{ID: "music-2.5", DisplayName: "Music 2.5"},
			{ID: "speech-02-hd", DisplayName: "Speech 02 HD"},
			{ID: "speech-02-turbo", DisplayName: "Speech 02 Turbo"},
		},
		"zai": {
			{ID: "glm-5.2", DisplayName: "GLM 5.2"},
			{ID: "glm-5.1", DisplayName: "GLM 5.1"},
			{ID: "glm-5-turbo", DisplayName: "GLM 5 Turbo"},
			{ID: "glm-5", DisplayName: "GLM 5"},
			{ID: "glm-4.7", DisplayName: "GLM 4.7"},
			{ID: "glm-4.7-flash", DisplayName: "GLM 4.7 Flash"},
			{ID: "glm-4.7-flashx", DisplayName: "GLM 4.7 FlashX"},
			{ID: "glm-4.6", DisplayName: "GLM 4.6"},
			{ID: "glm-4.5", DisplayName: "GLM 4.5"},
			{ID: "glm-4.5-air", DisplayName: "GLM 4.5 Air"},
			{ID: "glm-4.5-x", DisplayName: "GLM 4.5 X"},
			{ID: "glm-4.5-airx", DisplayName: "GLM 4.5 AirX"},
			{ID: "glm-4.5-flash", DisplayName: "GLM 4.5 Flash"},
			{ID: "glm-4-32b-0414-128k", DisplayName: "GLM 4 32B 0414 128K"},
		},
		"claude_cli": {
			{ID: "claude-opus-4-6", DisplayName: "Opus 4.6"},
			{ID: "claude-sonnet-4-6", DisplayName: "Sonnet 4.6"},
			{ID: "claude-haiku-4-5-20251001", DisplayName: "Haiku 4.5 (20251001)"},
			{ID: "sonnet[1m]", DisplayName: "Sonnet (1M context)"},
			{ID: "sonnet", DisplayName: "Sonnet"},
			{ID: "opus", DisplayName: "Opus"},
			{ID: "haiku", DisplayName: "Haiku"},
		},
		"acp": {
			{ID: "claude", DisplayName: "Claude"},
			{ID: "codex", DisplayName: "Codex"},
			{ID: "gemini", DisplayName: "Gemini"},
		},
		"chatgpt_oauth": {
			{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol"},
			{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6 Terra"},
			{ID: "gpt-5.5", DisplayName: "GPT-5.5"},
			{ID: "gpt-5.4", DisplayName: "GPT-5.4"},
			{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 Mini"},
			{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
			{ID: "gpt-5.3-codex-spark", DisplayName: "GPT-5.3 Codex Spark"},
			{ID: "gpt-5.2-codex", DisplayName: "GPT-5.2 Codex"},
			{ID: "gpt-5.2", DisplayName: "GPT-5.2"},
			{ID: "gpt-5.1-codex", DisplayName: "GPT-5.1 Codex"},
			{ID: "gpt-5.1-codex-max", DisplayName: "GPT-5.1 Codex Max"},
			{ID: "gpt-5.1-codex-mini", DisplayName: "GPT-5.1 Codex Mini"},
			{ID: "gpt-5.1", DisplayName: "GPT-5.1"},
		},
		"aimlapi": aimlapi,
	}
}

// TestBundledSnapshotMatchesLegacyCatalog is the seeding-parity guard: the shipped
// snapshot reproduces every hardcoded catalogue row for row, in order.
func TestBundledSnapshotMatchesLegacyCatalog(t *testing.T) {
	for providerType, want := range legacyCatalog(t) {
		t.Run(providerType, func(t *testing.T) {
			got := Bundled(providerType)
			if len(got) != len(want) {
				t.Fatalf("Bundled(%s) len = %d, want %d", providerType, len(got), len(want))
			}
			for i := range want {
				if got[i].ID != want[i].ID {
					t.Errorf("row %d: id = %q, want %q", i, got[i].ID, want[i].ID)
				}
				if got[i].DisplayName != want[i].DisplayName {
					t.Errorf("row %d (%s): display name = %q, want %q", i, got[i].ID, got[i].DisplayName, want[i].DisplayName)
				}
			}
		})
	}
}

// TestBundledSnapshotMatchesRegistrySeeds is the other half of the seeding-parity
// guard: the anthropic_native/openai rows reproduce the ModelRegistry seeds (the
// pre-phase-3 home of that knowledge) field for field. Catalog order is a
// sync.Map range, so this compares by id.
func TestBundledSnapshotMatchesRegistrySeeds(t *testing.T) {
	reg := providers.NewInMemoryRegistry()
	for _, tc := range []struct{ providerType, registryProvider string }{
		{"anthropic_native", "anthropic"},
		{"openai", "openai"},
	} {
		t.Run(tc.providerType, func(t *testing.T) {
			seeds := reg.Catalog(tc.registryProvider)
			byID := make(map[string]providers.ModelSpec, len(seeds))
			for _, seed := range seeds {
				byID[seed.ID] = seed
			}
			rows := Bundled(tc.providerType)
			if len(rows) != len(byID) {
				t.Fatalf("bundled rows = %d, registry seeds = %d", len(rows), len(byID))
			}
			for _, row := range rows {
				seed, ok := byID[row.ID]
				if !ok {
					t.Errorf("bundled row %q is not a registry seed", row.ID)
					continue
				}
				if row.ContextWindow == nil || *row.ContextWindow != seed.ContextWindow {
					t.Errorf("%s: context window = %v, want %d", row.ID, row.ContextWindow, seed.ContextWindow)
				}
				if row.MaxTokens == nil || *row.MaxTokens != seed.MaxTokens {
					t.Errorf("%s: max tokens = %v, want %d", row.ID, row.MaxTokens, seed.MaxTokens)
				}
				if row.Tokenizer != seed.TokenizerID {
					t.Errorf("%s: tokenizer = %q, want %q", row.ID, row.Tokenizer, seed.TokenizerID)
				}
				if row.Capabilities["reasoning"] != seed.Reasoning {
					t.Errorf("%s: reasoning = %v, want %v", row.ID, row.Capabilities["reasoning"], seed.Reasoning)
				}
				if row.Capabilities["vision"] != seed.Vision {
					t.Errorf("%s: vision = %v, want %v", row.ID, row.Capabilities["vision"], seed.Vision)
				}
			}
		})
	}
}

// TestBundledSnapshotZaiCodingSharesCatalog: the coding plan is the same list.
func TestBundledSnapshotZaiCodingSharesCatalog(t *testing.T) {
	zai, coding := Bundled("zai"), Bundled("zai_coding")
	if len(zai) != len(coding) || len(zai) == 0 {
		t.Fatalf("zai=%d zai_coding=%d", len(zai), len(coding))
	}
	for i := range zai {
		if zai[i].ID != coding[i].ID {
			t.Fatalf("row %d: %q vs %q", i, zai[i].ID, coding[i].ID)
		}
	}
}

// TestBundledSnapshotCarriesKnownMetadataOnly: context window / tokenizer are
// filled where the ModelRegistry seeds knew them, and left unset where the
// hardcoded catalogues only knew an id and a label.
func TestBundledSnapshotCarriesKnownMetadataOnly(t *testing.T) {
	anthropic := Bundled("anthropic_native")
	if len(anthropic) != 3 {
		t.Fatalf("anthropic rows = %d, want 3", len(anthropic))
	}
	opus := anthropic[0]
	if opus.ContextWindow == nil || *opus.ContextWindow != 200_000 {
		t.Errorf("claude-opus-4-6 context = %v, want 200000", opus.ContextWindow)
	}
	if opus.MaxTokens == nil || *opus.MaxTokens != 32_000 {
		t.Errorf("claude-opus-4-6 max tokens = %v, want 32000", opus.MaxTokens)
	}
	if opus.Tokenizer != "cl100k_base" {
		t.Errorf("claude-opus-4-6 tokenizer = %q", opus.Tokenizer)
	}
	if opus.Capabilities["vision"] != true || opus.Capabilities["reasoning"] != true {
		t.Errorf("claude-opus-4-6 capabilities = %v", opus.Capabilities)
	}
	haiku := anthropic[2]
	if haiku.Capabilities["reasoning"] != false {
		t.Errorf("claude-haiku-4-5 reasoning = %v, want an explicit false", haiku.Capabilities)
	}

	bailian := Bundled("bailian")
	if bailian[0].ContextWindow != nil || bailian[0].Capabilities != nil {
		t.Errorf("bailian metadata must stay unknown: %+v", bailian[0])
	}
}

func TestResolveType(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		wireAPI      string
		settings     string
		want         string
	}{
		{"ollama", "ollama", "ollama-native", "", TypeOllama},
		{"ollama cloud", "ollama_cloud", "ollama-native", "", TypeOllama},
		{"claude cli", "claude_cli", "cli-delegated", "", TypeStatic},
		{"acp", "acp", "cli-delegated", "", TypeStatic},
		{"chatgpt oauth", "chatgpt_oauth", "openai-codex-responses", "", TypeStatic},
		{"bailian", "bailian", "openai-completions", "", TypeStatic},
		{"zai coding", "zai_coding", "openai-completions", "", TypeStatic},
		{"aimlapi", "aimlapi", "openai-completions", "", TypeStatic},
		{"openrouter", "openrouter", "openai-completions", "", TypeProxy},
		{"openai compat", "openai_compat", "openai-completions", "", TypeOpenAIModelsList},
		{"anthropic", "anthropic_native", "anthropic-messages", "", TypeOpenAIModelsList},
		{"unknown brand", "yescale", "openai-completions", "", TypeOpenAIModelsList},
		{"unknown wire", "", "", "", TypeOpenAIModelsList},
		{"override litellm", "openai_compat", "openai-completions", `{"discovery":"litellm"}`, TypeLiteLLM},
		{"override static", "openai_compat", "openai-completions", `{"discovery":"static"}`, TypeStatic},
		{"bogus override ignored", "openai_compat", "openai-completions", `{"discovery":"nope"}`, TypeOpenAIModelsList},
		{"malformed settings", "openai_compat", "openai-completions", `{`, TypeOpenAIModelsList},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveType(tc.providerType, tc.wireAPI, json.RawMessage(tc.settings)); got != tc.want {
				t.Fatalf("ResolveType = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDiscoverFailureIsNeverAnEmptySlice pins the failure contract: a broken
// listing returns an error, so the caller can keep the previous rows and mark
// them stale instead of showing "no models".
func TestDiscoverFailureIsNeverAnEmptySlice(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(upstream.Close)

	reg := NewRegistry(nil)
	models, err := reg.Discover(context.Background(), ProviderRef{
		Name: "broken", ProviderType: "openai_compat", BaseURL: upstream.URL, APIKey: "k",
	})
	if err == nil {
		t.Fatalf("expected an error, got %d models", len(models))
	}
	if len(models) != 0 {
		t.Fatalf("models = %v, want none alongside the error", models)
	}
	if class := ClassOf(err); class != ClassHTTP {
		t.Fatalf("class = %q, want %q", class, ClassHTTP)
	}
}

// TestDiscoverWithoutAnImplementationIsUnsupported: a registry with no
// implementation for the resolved type must fail loudly (the caller keeps the
// cached rows) instead of pretending the provider has no models.
func TestDiscoverWithoutAnImplementationIsUnsupported(t *testing.T) {
	_, err := (&Registry{}).Discover(context.Background(), ProviderRef{Name: "x", ProviderType: "custom"})
	if err == nil {
		t.Fatal("expected an error for an unresolvable discovery implementation")
	}
	if class := ClassOf(err); class != ClassUnsupported {
		t.Fatalf("class = %q, want %q", class, ClassUnsupported)
	}
}

func TestOpenAIModelsListAuthPerWire(t *testing.T) {
	var gotPath, gotAuth, gotKey, gotVersion, gotUA string
	var handler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotKey, gotVersion, gotUA = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.Header.Get("User-Agent")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}, {"id": "m2", "owned_by": "vendor"}}})
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r) }))
	t.Cleanup(upstream.Close)

	reg := NewRegistry(nil)
	d, ok := reg.For("openai_compat", "openai-completions", nil)
	if !ok {
		t.Fatal("no implementation for openai_compat")
	}
	models, err := d.List(context.Background(), ProviderRef{
		Name: "p", ProviderType: "openai_compat", WireAPI: "openai-completions",
		BaseURL: upstream.URL, APIKey: "secret", ExtraHeaders: map[string]string{"User-Agent": "agent/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/models" {
		t.Errorf("path = %q, want /models", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUA != "agent/1" {
		t.Errorf("User-Agent = %q, want the vendor identity header", gotUA)
	}
	if len(models) != 2 || models[0].ID != "m1" {
		t.Fatalf("models = %+v", models)
	}

	// Anthropic wire: x-api-key + version, display_name from the payload.
	handler = func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotKey, gotVersion = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "claude-x", "display_name": "Claude X"}}})
	}
	ad, _ := reg.For("anthropic_native", "anthropic-messages", nil)
	models, err = ad.List(context.Background(), ProviderRef{Name: "a", ProviderType: "anthropic_native", WireAPI: "anthropic-messages", BaseURL: upstream.URL, APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "key" || gotVersion != "2023-06-01" || gotAuth != "" {
		t.Errorf("anthropic headers: key=%q version=%q auth=%q", gotKey, gotVersion, gotAuth)
	}
	if models[0].DisplayName != "Claude X" {
		t.Errorf("anthropic display name = %q", models[0].DisplayName)
	}
}

func TestOpenAIModelsListClassifiesAuthFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(upstream.Close)

	d := &openAIModelsList{}
	_, err := d.List(context.Background(), ProviderRef{ProviderType: "openai_compat", BaseURL: upstream.URL})
	if class := ClassOf(err); class != ClassAuth {
		t.Fatalf("class = %q, want %q (err=%v)", class, ClassAuth, err)
	}
}

func TestOpenAIModelsListClassifiesDecodeFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	t.Cleanup(upstream.Close)

	d := &openAIModelsList{}
	_, err := d.List(context.Background(), ProviderRef{ProviderType: "openai_compat", BaseURL: upstream.URL})
	if class := ClassOf(err); class != ClassDecode {
		t.Fatalf("class = %q, want %q (err=%v)", class, ClassDecode, err)
	}
}

// TestOpenAIModelsListEmptyUpstreamIsNotAFailure: an upstream that answers "no
// models" answered; that is not an error.
func TestOpenAIModelsListEmptyUpstreamIsNotAFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(upstream.Close)

	d := &openAIModelsList{}
	models, err := d.List(context.Background(), ProviderRef{ProviderType: "openai_compat", BaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %v, want empty", models)
	}
}

func TestDiscoveryGuardBlocksNonHTTPSchemes(t *testing.T) {
	d := &openAIModelsList{}
	_, err := d.List(context.Background(), ProviderRef{ProviderType: "openai_compat", BaseURL: "file:///etc/passwd"})
	if class := ClassOf(err); class != ClassInvalidURL {
		t.Fatalf("class = %q, want %q (err=%v)", class, ClassInvalidURL, err)
	}
}

func TestDiscoveryGuardDelegatesToCaller(t *testing.T) {
	guardCalled := false
	reg := NewRegistry(func(rawURL, providerType string) error {
		guardCalled = true
		if providerType != "openai_compat" {
			t.Errorf("provider type = %q", providerType)
		}
		return errors.New("blocked by policy")
	})
	_, err := reg.Discover(context.Background(), ProviderRef{Name: "p", ProviderType: "openai_compat", BaseURL: "https://api.example.com/v1"})
	if !guardCalled {
		t.Fatal("the caller's URL guard was not invoked")
	}
	if class := ClassOf(err); class != ClassInvalidURL {
		t.Fatalf("class = %q, want %q", class, ClassInvalidURL)
	}
}

func TestOllamaDiscoveryTagsAndShow(t *testing.T) {
	var showCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
				{"name": "gemma4:8b-it-q4_K_M", "details": map[string]string{"family": "gemma4", "parameter_size": "8.0B", "quantization_level": "Q4_K_M"}},
				{"name": "bare:latest", "details": map[string]string{}},
			}})
		case "/api/show":
			showCalls++
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Model == "bare:latest" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"gemma4.context_length": 8192}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	// A /v1-suffixed api_base must still hit /api/tags at the root.
	ref := ProviderRef{Name: "local", ProviderType: "ollama", WireAPI: "ollama-native", BaseURL: upstream.URL + "/v1"}
	models, err := (&ollamaDiscovery{}).List(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].DisplayName != "gemma4 8.0B Q4_K_M" {
		t.Errorf("display name = %q", models[0].DisplayName)
	}
	if models[0].ContextWindow == nil || *models[0].ContextWindow != 8192 {
		t.Errorf("context window = %v, want 8192 from /api/show", models[0].ContextWindow)
	}
	if models[1].DisplayName != "bare:latest" {
		t.Errorf("fallback display name = %q", models[1].DisplayName)
	}
	if models[1].ContextWindow != nil {
		t.Errorf("unavailable context window = %v, want nil (unknown, not the built-in default)", *models[1].ContextWindow)
	}
	if showCalls != 2 {
		t.Errorf("/api/show calls = %d, want 2", showCalls)
	}
}

func TestOllamaDiscoveryEmptyTagsIsNotAFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	t.Cleanup(upstream.Close)

	models, err := (&ollamaDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "ollama", BaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %v", models)
	}
}

func TestOllamaDiscoveryNon200IsAnError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	t.Cleanup(upstream.Close)

	models, err := (&ollamaDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "ollama", BaseURL: upstream.URL})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(models) != 0 {
		t.Fatalf("models = %v", models)
	}
	if class := ClassOf(err); class != ClassNotFound {
		t.Fatalf("class = %q, want %q", class, ClassNotFound)
	}
}

func TestStaticDiscoveryServesBundledAndErrorsWithoutOne(t *testing.T) {
	models, err := (&staticDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "zai"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != len(Bundled("zai")) {
		t.Fatalf("static models = %d, want %d", len(models), len(Bundled("zai")))
	}
	_, err = (&staticDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "no-such-brand"})
	if class := ClassOf(err); class != ClassUnsupported {
		t.Fatalf("class = %q, want %q", class, ClassUnsupported)
	}
}

func TestProxyDiscoveryFallsBackToAnthropicShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A proxy that fronts only the Anthropic listing.
		if r.Header.Get("x-api-key") != "k" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "anthropic/claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5"}}})
	}))
	t.Cleanup(upstream.Close)

	models, err := (&proxyDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "openrouter", BaseURL: upstream.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("models = %+v", models)
	}
}

func TestLiteLLMDiscoveryModelInfo(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/model/info" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"model_name": "gpt-5.5", "model_info": map[string]any{"max_input_tokens": 400_000, "max_output_tokens": 128_000}},
			{"model_name": "no-limits", "model_info": map[string]any{}},
		}})
	}))
	t.Cleanup(upstream.Close)

	models, err := (&liteLLMDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "litellm", BaseURL: upstream.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ContextWindow == nil || *models[0].ContextWindow != 400_000 {
		t.Errorf("context window = %v", models[0].ContextWindow)
	}
	if models[1].ContextWindow != nil || models[1].MaxTokens != nil {
		t.Errorf("unknown limits must stay nil: %+v", models[1])
	}
}

func TestLiteLLMDiscoveryFallsBackToModelsEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m1"}}})
	}))
	t.Cleanup(upstream.Close)

	models, err := (&liteLLMDiscovery{}).List(context.Background(), ProviderRef{ProviderType: "litellm", BaseURL: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "m1" {
		t.Fatalf("models = %+v", models)
	}
}

func TestFailedPreservesAnExistingClass(t *testing.T) {
	base := Failed(ClassAuth, errors.New("nope"))
	if got := ClassOf(Failed(ClassUnknown, base)); got != ClassAuth {
		t.Fatalf("class = %q, want the original %q", got, ClassAuth)
	}
}
