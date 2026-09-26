package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// exec_path chooses the executable the gateway runs for a cli-delegated
// provider, and it is authoritative over api_base. A create request must not be
// able to set it, or a caller could point the gateway at any binary without
// passing the CLI-executable check api_base has to pass.
func TestProvidersHandlerCreateRejectsClientSuppliedExecPath(t *testing.T) {
	token := setupProvidersAdminToken(t)
	providerStore := newMockProviderStore()
	handler := NewProvidersHandler(providerStore, newMockSecretsStore(), nil, "")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := `{
		"name": "evil-cli",
		"provider_type": "claude_cli",
		"api_base": "claude",
		"exec_path": "/usr/local/bin/evil",
		"enabled": true
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/providers", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d (body %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "exec_path") {
		t.Fatalf("response body = %q, want it to name exec_path", w.Body.String())
	}
	if len(providerStore.providers) != 0 {
		t.Fatalf("provider store mutated on a rejected create: %#v", providerStore.providers)
	}
}

// Update must filter exec_path (it is migration-written) while accepting the
// declaration columns, which store.ValidateProviderUpdates validates.
func TestProvidersHandlerUpdateFiltersExecPathAndAcceptsDeclaration(t *testing.T) {
	token := setupProvidersAdminToken(t)
	providerStore := newMockProviderStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		TenantID:     uuid.New(),
		Name:         "decl-anthropic",
		ProviderType: store.ProviderAnthropicNative,
		WireAPI:      store.WireAPIAnthropicMessages,
		AuthKind:     store.AuthKindAPIKey,
		Enabled:      true,
	}
	providerStore.providers[provider.Name] = provider

	handler := NewProvidersHandler(providerStore, newMockSecretsStore(), nil, "")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := `{"wire_api": "ollama-native", "auth_kind": "none", "exec_path": "/usr/local/bin/evil"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/providers/"+provider.ID.String(), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d (body %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if _, ok := providerStore.lastUpdates["exec_path"]; ok {
		t.Error("exec_path reached the store from an update request")
	}
	if got, _ := providerStore.lastUpdates["wire_api"].(string); got != store.WireAPIOllamaNative {
		t.Errorf("wire_api update = %q, want %q", got, store.WireAPIOllamaNative)
	}
	if got, _ := providerStore.lastUpdates["auth_kind"].(string); got != store.AuthKindNone {
		t.Errorf("auth_kind update = %q, want %q", got, store.AuthKindNone)
	}

	stored, err := providerStore.GetProvider(t.Context(), provider.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if stored.WireAPI != store.WireAPIOllamaNative || stored.AuthKind != store.AuthKindNone {
		t.Errorf("stored declaration = %q/%q, want %q/%q",
			stored.WireAPI, stored.AuthKind, store.WireAPIOllamaNative, store.AuthKindNone)
	}
	// The response must not echo the declaration as transport detail? It may
	// carry it, but never a client-supplied exec_path.
	var echo map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &echo); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := echo["exec_path"]; ok {
		t.Error("response echoed exec_path")
	}
}
