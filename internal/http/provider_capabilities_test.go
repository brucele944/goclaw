package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// dtoKeys returns the sorted top-level keys of a JSON object.
func dtoKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode JSON object: %v (raw=%s)", err, raw)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func assertKeySet(t *testing.T, label string, raw []byte, want []string) {
	t.Helper()
	got := dtoKeys(t, raw)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s key set = %v, want %v", label, got, want)
	}
}

// transportAndSecretKeys are the fields the capability DTO must never carry: a
// surface that renders the DTO must not be able to leak a base URL, an
// executable path, a credential, the settings blob or a compat injection.
var transportAndSecretKeys = []string{
	"api_base", "base_url", "exec_path", "api_key", "apikey", "authorization",
	"settings", "compat", "extra_body", "headers",
}

// TestProviderCapabilitiesDTOKeySet locks the DTO key set for a fully-populated
// provider. The shape is a contract with three surfaces (web UI, desktop UI,
// CLI), so a silent key change breaks a picker at runtime, and the leak
// assertion is what keeps transport detail out by construction.
func TestProviderCapabilitiesDTOKeySet(t *testing.T) {
	window, maxTokens := 128000, 8192
	fetchedAt := time.Now().UTC().Add(-time.Hour)
	provider := apiProviderCapability{
		ID:              "groq",
		ProviderID:      uuid.NewString(),
		Label:           "Groq",
		WireAPI:         store.WireAPIOpenAICompletions,
		AuthKind:        store.AuthKindAPIKey,
		ModelSource:     store.ModelSourceDiscovered,
		DefaultModelID:  "groq/llama-3.3-70b",
		Stale:           true,
		LastRefreshedAt: &fetchedAt,
		Models: []apiCapabilityModel{{
			ID:                   "groq/llama-3.3-70b",
			Label:                "Llama 3.3 70B",
			ContextWindow:        &window,
			MaxTokens:            &maxTokens,
			ThinkingLevels:       []string{"low", "medium"},
			DefaultThinkingLevel: "medium",
			Capabilities: apiCapabilityFlags{
				ToolCalling:     true,
				Vision:          true,
				StreamWithTools: true,
				CacheControl:    true,
			},
			Cost:  &modelCostDTO{Input: 0.59, Output: 0.79, Source: "row"},
			Stale: true,
		}},
	}

	raw, err := json.Marshal(provider)
	if err != nil {
		t.Fatalf("marshal provider DTO: %v", err)
	}
	assertKeySet(t, "provider", raw, []string{
		"id", "provider_id", "label", "wire_api", "auth_kind",
		"model_source", "default_model_id", "models", "stale", "last_refreshed_at",
	})

	var decoded struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if len(decoded.Models) != 1 {
		t.Fatalf("got %d models, want 1", len(decoded.Models))
	}
	assertKeySet(t, "model", decoded.Models[0], []string{
		"id", "label", "context_window", "max_tokens", "thinking_levels",
		"default_thinking_level", "capabilities", "cost", "stale",
	})

	for _, forbidden := range transportAndSecretKeys {
		if strings.Contains(string(raw), `"`+forbidden+`":`) {
			t.Fatalf("capability DTO must not carry %q: %s", forbidden, raw)
		}
	}
	for _, sentinel := range []string{"https://", "/usr/local/bin", "sk-", "bearer ", "gsk-"} {
		if strings.Contains(strings.ToLower(string(raw)), sentinel) {
			t.Fatalf("capability DTO must not carry transport detail %q: %s", sentinel, raw)
		}
	}
}

// TestProviderCapabilitiesEndpointListsEnabledProviders drives the endpoint
// through the router: identity, declaration and per-model capabilities for an
// enabled provider, and nothing for a disabled one.
func TestProviderCapabilitiesEndpointListsEnabledProviders(t *testing.T) {
	token := setupProvidersAdminToken(t)
	providerStore := newCatalogStore()

	enabled := &store.LLMProviderData{
		Name:         "groq",
		DisplayName:  "Groq",
		ProviderType: store.ProviderGroq,
		WireAPI:      store.WireAPIOpenAICompletions,
		AuthKind:     store.AuthKindAPIKey,
		APIKey:       "gsk-test",
		APIBase:      "https://sentinel-base.invalid/v1",
		ExecPath:     "/sentinel/exec/path",
		Settings:     json.RawMessage(`{"sentinel_setting":"sentinel-settings-value"}`),
		Enabled:      true,
	}
	if err := providerStore.CreateProvider(t.Context(), enabled); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	disabled := &store.LLMProviderData{
		Name:         "retired",
		ProviderType: store.ProviderGroq,
		WireAPI:      store.WireAPIOpenAICompletions,
		AuthKind:     store.AuthKindAPIKey,
		Enabled:      false,
	}
	if err := providerStore.CreateProvider(t.Context(), disabled); err != nil {
		t.Fatalf("create disabled provider: %v", err)
	}

	window, maxTokens := 128000, 8192
	providerStore.seedRow(enabled.ID, store.LLMModel{
		ProviderID:    enabled.ID,
		ModelID:       "llama-3.3-70b",
		DisplayName:   strPtr("Llama 3.3 70B"),
		ContextWindow: &window,
		MaxTokens:     &maxTokens,
		Source:        store.ModelSourceDiscovered,
		Enabled:       true,
		Capabilities:  json.RawMessage(`{"tool_calling":true,"vision":false}`),
	})

	_, mux := newCatalogHandler(t, providerStore)

	status, body := rawBodyGET(t, mux, token, "/v1/providers/capabilities")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}

	var resp apiProviderCapabilitiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("got %d providers, want 1 (disabled providers must be omitted): %s", len(resp.Providers), body)
	}

	got := resp.Providers[0]
	if got.ID != "groq" || got.Label != "Groq" {
		t.Fatalf("provider identity = %q/%q, want groq/Groq", got.ID, got.Label)
	}
	if got.WireAPI != store.WireAPIOpenAICompletions || got.AuthKind != store.AuthKindAPIKey {
		t.Fatalf("declaration = %q/%q", got.WireAPI, got.AuthKind)
	}
	if got.ProviderID != enabled.ID.String() {
		t.Fatalf("provider_id = %q, want %q", got.ProviderID, enabled.ID)
	}

	var seeded *apiCapabilityModel
	for i := range got.Models {
		if got.Models[i].ID == "groq/llama-3.3-70b" {
			seeded = &got.Models[i]
			break
		}
	}
	if seeded == nil {
		t.Fatalf("catalogue row missing from the DTO: %s", body)
	}
	if seeded.Label != "Llama 3.3 70B" {
		t.Fatalf("label = %q, want the row display name", seeded.Label)
	}
	if seeded.ContextWindow == nil || *seeded.ContextWindow != window {
		t.Fatalf("context_window = %v, want %d", seeded.ContextWindow, window)
	}
	if !seeded.Capabilities.ToolCalling {
		t.Fatalf("tool_calling = false, want the row's declared true: %+v", seeded.Capabilities)
	}
	if seeded.Capabilities.Vision {
		t.Fatalf("vision = true, want the row's declared false: %+v", seeded.Capabilities)
	}

	for _, forbidden := range transportAndSecretKeys {
		if strings.Contains(string(body), `"`+forbidden+`":`) {
			t.Fatalf("endpoint leaked %q: %s", forbidden, body)
		}
	}
	// Values, not just field names: a future field that nests transport detail
	// under a different key still must not carry these strings out.
	for _, sentinel := range []string{
		"gsk-test", "sentinel-base.invalid", "/sentinel/exec/path", "sentinel-settings-value",
	} {
		if strings.Contains(string(body), sentinel) {
			t.Fatalf("endpoint leaked transport detail %q: %s", sentinel, body)
		}
	}
}

// TestProviderCapabilitiesEndpointFiltersByIdentity covers the ?id= filter: a
// picker for one agent must be able to ask for exactly one provider, and an
// unknown reference must answer with an empty list rather than every provider.
func TestProviderCapabilitiesEndpointFiltersByIdentity(t *testing.T) {
	token := setupProvidersAdminToken(t)
	providerStore := newCatalogStore()
	for _, name := range []string{"alpha", "beta"} {
		p := &store.LLMProviderData{
			Name:         name,
			ProviderType: store.ProviderGroq,
			WireAPI:      store.WireAPIOpenAICompletions,
			AuthKind:     store.AuthKindAPIKey,
			APIKey:       "k",
			Enabled:      true,
		}
		if err := providerStore.CreateProvider(t.Context(), p); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	_, mux := newCatalogHandler(t, providerStore)

	status, body := rawBodyGET(t, mux, token, "/v1/providers/capabilities?id=beta")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var resp apiProviderCapabilitiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Providers) != 1 || resp.Providers[0].ID != "beta" {
		t.Fatalf("filter returned %d providers: %s", len(resp.Providers), body)
	}

	status, body = rawBodyGET(t, mux, token, "/v1/providers/capabilities?id=missing")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var empty apiProviderCapabilitiesResponse
	if err := json.Unmarshal(body, &empty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(empty.Providers) != 0 {
		t.Fatalf("unknown id returned %d providers: %s", len(empty.Providers), body)
	}
}

// TestProviderCapabilitiesRouteIsRegistered guards the route itself: a literal
// path segment must still win over the /v1/providers/{id} wildcard, or the
// endpoint would silently answer as "provider not found".
func TestProviderCapabilitiesRouteIsRegistered(t *testing.T) {
	token := setupProvidersAdminToken(t)
	providerStore := newCatalogStore()
	_, mux := newCatalogHandler(t, providerStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/providers/capabilities", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"providers"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
