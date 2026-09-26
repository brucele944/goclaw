package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestRegisterProvidersFromDBUsesDeclaredWireAPI is the phase-2 acceptance case:
// a row that declares wire_api = 'openai-completions' plus its own api_base is
// dispatched with no code change, and the request reaches that endpoint on the
// Chat Completions path.
func TestRegisterProvidersFromDBUsesDeclaredWireAPI(t *testing.T) {
	var (
		capturedPath  string
		capturedModel string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedModel = body.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(upstream.Close)

	tenantID := uuid.New()
	provStore := gatewayProvidersStoreStub{providers: []store.LLMProviderData{{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		TenantID:     tenantID,
		Name:         "bespoke-gateway",
		ProviderType: "bespoke_proxy", // not a catalogued brand: only wire_api matters
		WireAPI:      store.WireAPIOpenAICompletions,
		APIKey:       "token",
		APIBase:      upstream.URL,
		Enabled:      true,
	}}}

	registry := providers.NewRegistry(nil)
	registerProvidersFromDB(registry, provStore, nil, "", "", nil, &config.Config{}, providers.NewInMemoryRegistry(), nil)

	prov, err := registry.GetForTenant(tenantID, "bespoke-gateway")
	if err != nil {
		t.Fatalf("GetForTenant() error = %v", err)
	}
	openai, ok := prov.(*providers.OpenAIProvider)
	if !ok {
		t.Fatalf("registered provider = %T, want *providers.OpenAIProvider", prov)
	}
	if got := openai.APIBase(); got != upstream.URL {
		t.Fatalf("APIBase() = %q, want the declared api_base %q", got, upstream.URL)
	}
	if _, err := prov.Chat(context.Background(), providers.ChatRequest{
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
		Model:    "gpt-4o",
	}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if capturedPath != "/chat/completions" {
		t.Fatalf("captured path = %q, want /chat/completions", capturedPath)
	}
	if capturedModel != "gpt-4o" {
		t.Fatalf("captured model = %q, want gpt-4o", capturedModel)
	}
}

// TestRegisterProvidersFromDBAppliesBrandCatalogDefaults: an empty api_base is
// resolved from the brand catalog, and a brand that reflects provider_type keeps
// doing so. Both used to live in the registration switch.
func TestRegisterProvidersFromDBAppliesBrandCatalogDefaults(t *testing.T) {
	tenantID := uuid.New()
	provStore := gatewayProvidersStoreStub{providers: []store.LLMProviderData{
		{
			BaseModel:    store.BaseModel{ID: uuid.New()},
			TenantID:     tenantID,
			Name:         "db-zai",
			ProviderType: store.ProviderZai,
			WireAPI:      store.WireAPIOpenAICompletions,
			APIKey:       "zai-token",
			Enabled:      true,
		},
		{
			BaseModel:    store.BaseModel{ID: uuid.New()},
			TenantID:     tenantID,
			Name:         "db-kimi",
			ProviderType: store.ProviderKimiCoding,
			WireAPI:      store.WireAPIOpenAICompletions,
			APIKey:       "kimi-token",
			Enabled:      true,
		},
	}}

	registry := providers.NewRegistry(nil)
	registerProvidersFromDB(registry, provStore, nil, "", "", nil, &config.Config{}, providers.NewInMemoryRegistry(), nil)

	zai, err := registry.GetForTenant(tenantID, "db-zai")
	if err != nil {
		t.Fatalf("GetForTenant(db-zai) error = %v", err)
	}
	zaiOpenAI := zai.(*providers.OpenAIProvider)
	if got := zaiOpenAI.APIBase(); got != store.ZaiDefaultAPIBase {
		t.Fatalf("db-zai APIBase() = %q, want %q", got, store.ZaiDefaultAPIBase)
	}
	if got := zaiOpenAI.DefaultModel(); got != store.ZaiDefaultModel {
		t.Fatalf("db-zai DefaultModel() = %q, want %q", got, store.ZaiDefaultModel)
	}

	kimi, err := registry.GetForTenant(tenantID, "db-kimi")
	if err != nil {
		t.Fatalf("GetForTenant(db-kimi) error = %v", err)
	}
	kimiOpenAI := kimi.(*providers.OpenAIProvider)
	if got := kimiOpenAI.APIBase(); got != store.KimiCodingDefaultAPIBase {
		t.Fatalf("db-kimi APIBase() = %q, want %q", got, store.KimiCodingDefaultAPIBase)
	}
	if got := kimiOpenAI.ExtraHeaders()["User-Agent"]; got != store.KimiCodingRequiredUserAgent {
		t.Fatalf("db-kimi User-Agent = %q, want %q", got, store.KimiCodingRequiredUserAgent)
	}
}

// TestRegisterProvidersFromDBRejectsUnknownWireAPI pins the "no silent default"
// contract: an unregistered wire_api is refused with a logged error naming the
// provider, instead of quietly becoming OpenAI-compatible.
func TestRegisterProvidersFromDBRejectsUnknownWireAPI(t *testing.T) {
	tenantID := uuid.New()
	provStore := gatewayProvidersStoreStub{providers: []store.LLMProviderData{{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		TenantID:     tenantID,
		Name:         "mystery-provider",
		ProviderType: store.ProviderOpenAICompat,
		WireAPI:      "definitely-not-a-wire-api",
		APIKey:       "token",
		Enabled:      true,
	}}}

	logs := captureSlog(t)
	registry := providers.NewRegistry(nil)
	registerProvidersFromDB(registry, provStore, nil, "", "", nil, &config.Config{}, providers.NewInMemoryRegistry(), nil)

	if _, err := registry.GetForTenant(tenantID, "mystery-provider"); err == nil {
		t.Fatal("a row with an unregistered wire_api must not be registered")
	}
	got := logs.String()
	if !strings.Contains(got, "provider.wire_api.unknown") {
		t.Fatalf("log output = %q, want the provider.wire_api.unknown event", got)
	}
	for _, want := range []string{"mystery-provider", "definitely-not-a-wire-api"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log output = %q, want it to name %q", got, want)
		}
	}
}

// TestRegisterProvidersFromDBAppliesProviderTimeout proves the per-provider
// settings.timeout_sec reaches the chat path: a 30 s stalled upstream is
// abandoned after the declared timeout, not after the transport's 300 s default.
func TestRegisterProvidersFromDBAppliesProviderTimeout(t *testing.T) {
	stalled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stalled:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(func() {
		close(stalled)
		upstream.Close()
	})

	tenantID := uuid.New()
	provStore := gatewayProvidersStoreStub{providers: []store.LLMProviderData{{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		TenantID:     tenantID,
		Name:         "slow-provider",
		ProviderType: store.ProviderOpenAICompat,
		WireAPI:      store.WireAPIOpenAICompletions,
		APIKey:       "token",
		APIBase:      upstream.URL,
		Settings:     json.RawMessage(`{"timeout_sec":5}`),
		Enabled:      true,
	}}}

	registry := providers.NewRegistry(nil)
	registerProvidersFromDB(registry, provStore, nil, "", "", nil, &config.Config{}, providers.NewInMemoryRegistry(), nil)

	prov, err := registry.GetForTenant(tenantID, "slow-provider")
	if err != nil {
		t.Fatalf("GetForTenant() error = %v", err)
	}
	start := time.Now()
	_, err = prov.Chat(context.Background(), providers.ChatRequest{
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Chat() against a stalled 30s upstream should fail")
	}
	if elapsed < 4*time.Second {
		t.Fatalf("Chat() failed after %v, want it to wait out the declared 5s deadline", elapsed)
	}
	if elapsed >= 25*time.Second {
		t.Fatalf("Chat() took %v, want the declared 5s timeout to bound it (not the transport default)", elapsed)
	}
}

// captureSlog redirects the default logger into a buffer for the duration of a
// test. Tests using it must not run in parallel.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}
