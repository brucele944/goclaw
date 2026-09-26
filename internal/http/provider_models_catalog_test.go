package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	usagecaps "github.com/nextlevelbuilder/goclaw/internal/usage/caps"
)

// catalogStore is the providers mock with a real in-memory llm_models store, so
// seeding, merging and the served catalogue can be asserted end to end.
type catalogStore struct {
	*mockProviderStore
	mu     sync.Mutex
	models map[uuid.UUID][]store.LLMModel
}

func newCatalogStore() *catalogStore {
	return &catalogStore{mockProviderStore: newMockProviderStore(), models: make(map[uuid.UUID][]store.LLMModel)}
}

func (s *catalogStore) ListModels(_ context.Context, providerID uuid.UUID) ([]store.LLMModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := append([]store.LLMModel(nil), s.models[providerID]...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ModelID < rows[j].ModelID })
	return rows, nil
}

func (s *catalogStore) UpsertModels(_ context.Context, providerID uuid.UUID, models []store.LLMModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.models[providerID]
	for _, in := range models {
		store.FillModelDefaults(&in)
		replaced := false
		for i := range rows {
			if rows[i].ModelID != in.ModelID {
				continue
			}
			enabled, id := rows[i].Enabled, rows[i].ID
			rows[i] = in
			rows[i].Enabled = enabled
			rows[i].ID = id
			replaced = true
			break
		}
		if !replaced {
			if in.ID == uuid.Nil {
				in.ID = uuid.New()
			}
			in.Enabled = true
			rows = append(rows, in)
		}
	}
	s.models[providerID] = rows
	return nil
}

func (s *catalogStore) SetModelEnabled(_ context.Context, providerID uuid.UUID, modelID string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.models[providerID] {
		if s.models[providerID][i].ModelID == modelID {
			s.models[providerID][i].Enabled = enabled
			return nil
		}
	}
	return errors.New("model not found")
}

// seedRow inserts a catalogue row directly (simulating a previous discovery or an
// operator edit).
func (s *catalogStore) seedRow(providerID uuid.UUID, row store.LLMModel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	s.models[providerID] = append(s.models[providerID], row)
}

func (s *catalogStore) rows(providerID uuid.UUID) []store.LLMModel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]store.LLMModel(nil), s.models[providerID]...)
}

// allowPrivateProviderURLs lets a test point a remote-type provider at an
// httptest loopback server: discovery enforces the same SSRF gate provider
// create and verify use, so the test has to opt in like an operator would.
func allowPrivateProviderURLs(t *testing.T) {
	t.Helper()
	saveAndRestoreGlobals(t)
	allowPrivateProviderURLsFn = func() bool { return true }
}

// rawBodyGET returns the status and the raw body of a GET request.
func rawBodyGET(t *testing.T, mux *http.ServeMux, token, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func newCatalogHandler(t *testing.T, s store.ProviderStore) (*ProvidersHandler, *http.ServeMux) {
	t.Helper()
	handler := NewProvidersHandler(s, newMockSecretsStore(), nil, "")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return handler, mux
}

func modelsListGET(t *testing.T, mux *http.ServeMux, token string) modelsEndpointResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp modelsEndpointResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func rawModelsGET(t *testing.T, mux *http.ServeMux, token, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestListProviderModelsSeedsTheBundledCatalog: the pre-phase hardcoded catalog is
// now persisted as source='bundled' rows and served from them, in snapshot order.
func TestListProviderModelsSeedsTheBundledCatalog(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "zai",
		ProviderType: store.ProviderZai,
		APIKey:       "token",
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	_, mux := newCatalogHandler(t, st)

	resp := providerModelsRequest(t, mux, provider.ID, token)
	want := discovery.Bundled(store.ProviderZai)
	if len(resp.Models) != len(want) {
		t.Fatalf("models = %d, want %d", len(resp.Models), len(want))
	}
	for i := range want {
		if resp.Models[i].ID != want[i].ID {
			t.Fatalf("row %d = %q, want %q", i, resp.Models[i].ID, want[i].ID)
		}
		if resp.Models[i].Name != want[i].DisplayName {
			t.Fatalf("row %d name = %q, want %q", i, resp.Models[i].Name, want[i].DisplayName)
		}
	}
	if resp.Stale || resp.Error != "" || resp.Fetched {
		t.Errorf("static catalog must be served without a fetch: %+v", resp)
	}

	rows := st.rows(provider.ID)
	if len(rows) != len(want) {
		t.Fatalf("persisted rows = %d, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		if row.Source != store.ModelSourceBundled {
			t.Fatalf("row %s source = %q, want bundled", row.ModelID, row.Source)
		}
	}
}

// TestListProviderModelsDiscoveryFailureIsStaleNotSilentEmpty replaces the old
// "return an empty list and log it" behaviour: a broken upstream keeps the cached
// rows, marks them stale and classifies the failure.
func TestListProviderModelsDiscoveryFailureIsStaleNotSilentEmpty(t *testing.T) {
	token := setupProvidersAdminToken(t)
	allowPrivateProviderURLs(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(upstream.Close)

	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "custom",
		ProviderType: store.ProviderOpenAICompat,
		WireAPI:      "openai-completions",
		APIBase:      upstream.URL,
		APIKey:       "token",
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	// A previously discovered row must survive the outage.
	st.seedRow(provider.ID, store.LLMModel{
		ModelID: "local-model", DisplayName: strPtr("Local Model"),
		Source: store.ModelSourceDiscovered, Enabled: true,
	})
	_, mux := newCatalogHandler(t, st)

	resp := providerModelsRequest(t, mux, provider.ID, token)
	if !resp.Stale {
		t.Error("stale = false, want true when discovery failed")
	}
	if resp.Error == "" {
		t.Error("error is empty, want the classified failure message")
	}
	if resp.ErrorClass != discovery.ClassAuth {
		t.Errorf("error_class = %q, want %q", resp.ErrorClass, discovery.ClassAuth)
	}
	if len(resp.Models) != 1 || resp.Models[0].ID != "local-model" {
		t.Fatalf("models = %+v, want the cached row", resp.Models)
	}
}

// TestListProviderModelsRefreshForcesAFetchAndFingerprintCachesIt: an unchanged
// api_base reuses the cache, a changed one re-fetches.
func TestListProviderModelsRefreshForcesAFetchAndFingerprintCachesIt(t *testing.T) {
	token := setupProvidersAdminToken(t)
	allowPrivateProviderURLs(t)
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "upstream-a"}, {"id": "upstream-b"}}})
	}))
	t.Cleanup(upstream.Close)

	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "proxy",
		ProviderType: store.ProviderOpenAICompat,
		WireAPI:      "openai-completions",
		APIBase:      upstream.URL,
		APIKey:       "k",
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	_, mux := newCatalogHandler(t, st)

	first := providerModelsRequest(t, mux, provider.ID, token)
	if !first.Fetched || calls != 1 {
		t.Fatalf("first call: fetched=%v calls=%d, want a fetch", first.Fetched, calls)
	}
	if len(first.Models) != 2 {
		t.Fatalf("models = %+v", first.Models)
	}

	second := providerModelsRequest(t, mux, provider.ID, token)
	if second.Fetched || calls != 1 {
		t.Fatalf("second call: fetched=%v calls=%d, want the cached catalog", second.Fetched, calls)
	}
	if len(second.Models) != 2 || second.Models[0].ID != "upstream-a" {
		t.Fatalf("cached models = %+v", second.Models)
	}

	// A changed api_base invalidates the fingerprint: the next list re-fetches.
	if err := st.UpdateProvider(t.Context(), provider.ID, map[string]any{"api_base": upstream.URL + "/moved"}); err != nil {
		t.Fatal(err)
	}
	third := providerModelsRequest(t, mux, provider.ID, token)
	if !third.Fetched || calls != 2 {
		t.Fatalf("after api_base change: fetched=%v calls=%d, want a refetch", third.Fetched, calls)
	}
}

// TestListModelsIsTenantScopedAndHidesDisabledModels: only the caller's enabled
// providers and enabled models are listed.
func TestListModelsIsTenantScopedAndHidesDisabledModels(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()

	enabled := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "zai", ProviderType: store.ProviderZai,
		APIKey: "k", Enabled: true,
	}
	disabledProvider := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "minimax", ProviderType: store.ProviderMiniMax,
		APIKey: "k", Enabled: false,
	}
	if err := st.CreateProvider(t.Context(), enabled); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProvider(t.Context(), disabledProvider); err != nil {
		t.Fatal(err)
	}
	// A disabled model row must not surface anywhere.
	st.seedRow(enabled.ID, store.LLMModel{ModelID: "retired", Source: store.ModelSourceBundled, Enabled: false})

	_, mux := newCatalogHandler(t, st)
	resp := modelsListGET(t, mux, token)
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Fatal("no models listed for the tenant's enabled provider")
	}
	seen := map[string]bool{}
	for _, item := range resp.Data {
		seen[item.Provider] = true
		if item.Provider == "minimax" {
			t.Error("a disabled provider was listed")
		}
		if item.Model == "retired" {
			t.Error("a disabled model was listed")
		}
		if item.OwnedBy != item.Provider {
			t.Errorf("owned_by = %q, provider = %q", item.OwnedBy, item.Provider)
		}
		if item.ID != item.Provider+"/"+item.Model {
			t.Errorf("id = %q, want provider/model identity", item.ID)
		}
	}
	if !seen["zai"] {
		t.Error("the enabled provider's models are missing")
	}
}

// TestListModelsExposesNoTransportSecrets is the key-set guard: provider
// credentials, exec paths, settings and compat bodies must not be reachable
// through either models endpoint.
func TestListModelsExposesNoTransportSecrets(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel:       store.BaseModel{ID: uuid.New()},
		Name:            "secretive",
		ProviderType:    store.ProviderOpenAICompat,
		APIBase:         "https://api.example.com/v1",
		APIKey:          "sk-super-secret",
		ExecPath:        "/usr/local/bin/agent",
		Settings:        json.RawMessage(`{"timeout_sec":5,"discovered_models_authoritative":true}`),
		Enabled:         true,
		TenantID:        uuid.New(),
		SettingsVersion: 1,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	st.seedRow(provider.ID, store.LLMModel{
		ModelID:      "leaky",
		DisplayName:  strPtr("Leaky"),
		Source:       store.ModelSourceOperator,
		Compat:       json.RawMessage(`{"extra_body":{"api_key":"sk-model-secret"},"headers":{"X-Token":"t"},"dialect":"openai"}`),
		Capabilities: json.RawMessage(`{"vision":true}`),
		Enabled:      true,
	})

	_, mux := newCatalogHandler(t, st)

	code, listBody := rawBodyGET(t, mux, token, "/v1/models")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	assertNoSecretKeys(t, listBody)

	code, detailBody := rawBodyGET(t, mux, token, "/v1/models/secretive/leaky")
	if code != http.StatusOK {
		t.Fatalf("detail status = %d", code)
	}
	assertNoSecretKeys(t, detailBody)
	var detail struct {
		Compat *struct {
			Keys []string `json:"keys"`
		} `json:"compat"`
	}
	if err := json.Unmarshal(detailBody, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Compat == nil || len(detail.Compat.Keys) != 1 || detail.Compat.Keys[0] != "dialect" {
		t.Fatalf("compat summary = %+v, want only the non-secret key", detail.Compat)
	}
}

// assertNoSecretKeys walks the response and fails on any secret-bearing key.
func assertNoSecretKeys(t *testing.T, body []byte) {
	t.Helper()
	forbidden := map[string]bool{
		"api_base": true, "exec_path": true, "api_key": true, "settings": true,
		"extra_body": true, "headers": true, "authorization": true,
	}
	var walk func(value any, path string)
	walk = func(value any, path string) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if forbidden[key] {
					t.Errorf("secret key %q leaked at %s", key, path)
				}
				walk(child, path+"."+key)
			}
		case []any:
			for _, child := range v {
				walk(child, path+"[]")
			}
		case string:
			if v == "sk-super-secret" || v == "sk-model-secret" {
				t.Errorf("secret value leaked at %s", path)
			}
		}
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	walk(decoded, "$")
}

// TestModelCapabilitiesDistinguishVisionThroughBothEndpoints: the fixture pair
// the phase asks for — one row with vision=true, one with vision=false.
func TestModelCapabilitiesDistinguishVisionThroughBothEndpoints(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "caps", ProviderType: store.ProviderOpenAICompat,
		APIKey: "k", Enabled: true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	st.seedRow(provider.ID, store.LLMModel{
		ModelID: "sees", Source: store.ModelSourceOperator, Enabled: true,
		Capabilities: json.RawMessage(`{"vision":true}`),
	})
	st.seedRow(provider.ID, store.LLMModel{
		ModelID: "blind", Source: store.ModelSourceOperator, Enabled: true,
		Capabilities: json.RawMessage(`{"vision":false}`),
	})

	_, mux := newCatalogHandler(t, st)
	list := modelsListGET(t, mux, token)
	seen := map[string]any{}
	for _, item := range list.Data {
		seen[item.Model] = item.Capabilities["vision"]
	}
	if seen["sees"] != true {
		t.Errorf("sees vision = %v, want true", seen["sees"])
	}
	if vision, ok := seen["blind"]; !ok || vision != false {
		t.Errorf("blind vision = %v (present=%v), want an explicit false", vision, ok)
	}

	for model, want := range map[string]bool{"sees": true, "blind": false} {
		code, body := rawModelsGET(t, mux, token, "/v1/models/caps/"+model)
		if code != http.StatusOK {
			t.Fatalf("detail %s status = %d", model, code)
		}
		caps, _ := body["capabilities"].(map[string]any)
		if caps["vision"] != want {
			t.Errorf("detail %s vision = %v, want %v", model, caps["vision"], want)
		}
		if body["model"] != model || body["provider"] != "caps" || body["id"] != "caps/"+model {
			t.Errorf("detail identity = %+v", body)
		}
	}
}

// TestModelDetailCostFallsBackToThePricingCatalog: a row with NULL cost is
// priced from the synced catalog, and a row that declares its own cost keeps it.
func TestModelDetailCostFallsBackToThePricingCatalog(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "priced", ProviderType: store.ProviderOpenAICompat,
		APIKey: "k", Enabled: true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	st.seedRow(provider.ID, store.LLMModel{ModelID: "gpt-5.5", Source: store.ModelSourceBundled, Enabled: true})
	ownInput, ownOutput := 1.5, 6.0
	st.seedRow(provider.ID, store.LLMModel{
		ModelID: "operator-priced", Source: store.ModelSourceOperator, Enabled: true,
		CostInput: &ownInput, CostOutput: &ownOutput,
	})

	handler, mux := newCatalogHandler(t, st)
	handler.SetUsageCapService(usagecaps.NewService(&catalogPricingStore{}, st))

	code, body := rawModelsGET(t, mux, token, "/v1/models/priced/gpt-5.5")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	cost, _ := body["cost"].(map[string]any)
	if cost == nil {
		t.Fatalf("cost missing for a NULL-cost row: %+v", body)
	}
	if cost["source"] != "pricing_catalog" {
		t.Errorf("cost source = %v, want pricing_catalog", cost["source"])
	}
	if cost["input"] != 3.0 || cost["output"] != 15.0 {
		t.Errorf("cost = %+v, want the catalog rates per 1M tokens", cost)
	}

	code, body = rawModelsGET(t, mux, token, "/v1/models/priced/operator-priced")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	cost, _ = body["cost"].(map[string]any)
	if cost == nil || cost["source"] != "row" || cost["input"] != 1.5 {
		t.Errorf("operator cost = %+v, want the row's own price", cost)
	}
}

// catalogPricingStore is a pricing catalog with one per-token entry, which is all
// the cost fallback reads. The other UsageCapStore methods are unused here.
type catalogPricingStore struct{}

func (catalogPricingStore) UpsertPricingCatalog(context.Context, []store.UsagePricingCatalogEntry) (int, error) {
	return 0, nil
}
func (catalogPricingStore) ListPricingCatalog(context.Context, store.UsagePricingQuery) ([]store.UsagePricingCatalogEntry, error) {
	return nil, nil
}
func (catalogPricingStore) PutPricingOverride(context.Context, *store.UsagePricingOverride) error {
	return nil
}
func (catalogPricingStore) ListPricingOverrides(context.Context, store.UsagePricingQuery) ([]store.UsagePricingOverride, error) {
	return nil, nil
}
func (catalogPricingStore) DeletePricingOverride(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (catalogPricingStore) ResolvePricing(_ context.Context, _, _ uuid.UUID, providerName, _ string, modelID string) (*store.ResolvedUsagePricing, error) {
	if providerName != "priced" || modelID != "gpt-5.5" {
		return nil, errors.New("no pricing")
	}
	in, out := "0.000003", "0.000015"
	return &store.ResolvedUsagePricing{
		ModelID: modelID,
		Source:  "catalog",
		Pricing: store.UsagePricingFields{Input: &in, Output: &out},
	}, nil
}
func (catalogPricingStore) CreateUsageCapPolicy(context.Context, *store.UsageCapPolicy) error {
	return nil
}
func (catalogPricingStore) ListUsageCapPolicies(context.Context, store.UsageCapScope, bool) ([]store.UsageCapPolicy, error) {
	return nil, nil
}
func (catalogPricingStore) UpdateUsageCapPolicy(context.Context, uuid.UUID, uuid.UUID, store.UsageCapPolicyPatch) (*store.UsageCapPolicy, error) {
	return nil, nil
}
func (catalogPricingStore) DeleteUsageCapPolicy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (catalogPricingStore) ReserveUsage(context.Context, store.UsageReserveRequest, []store.UsageCapPolicy) (*store.UsageReservationResult, error) {
	return nil, nil
}
func (catalogPricingStore) ReconcileUsage(context.Context, store.UsageReconcileRequest) error {
	return nil
}
func (catalogPricingStore) ListUsageCapUtilization(context.Context, uuid.UUID) ([]store.UsageCapUtilization, error) {
	return nil, nil
}
func (catalogPricingStore) ListUsageCapEvents(context.Context, uuid.UUID, int) ([]store.UsageCapEvent, error) {
	return nil, nil
}
func (catalogPricingStore) InsertUsageCapEvent(context.Context, *store.UsageCapEvent) error {
	return nil
}

// TestModelDetailUnknownModelIsNotFound and multi-segment ids stay addressable.
func TestModelDetailRouting(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "openrouter", ProviderType: store.ProviderOpenAICompat,
		APIKey: "k", Enabled: true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	st.seedRow(provider.ID, store.LLMModel{
		ModelID: "openai/gpt-5.5", Source: store.ModelSourceDiscovered, Enabled: true,
		StaticFingerprint: strPtr("fp"),
	})
	_, mux := newCatalogHandler(t, st)

	code, body := rawModelsGET(t, mux, token, "/v1/models/openrouter/openai/gpt-5.5")
	if code != http.StatusOK {
		t.Fatalf("multi-segment id status = %d (%v)", code, body)
	}
	if body["model"] != "openai/gpt-5.5" {
		t.Errorf("model = %v", body["model"])
	}

	if code, _ = rawModelsGET(t, mux, token, "/v1/models/openrouter/nope"); code != http.StatusNotFound {
		t.Errorf("unknown model status = %d, want 404", code)
	}
	if code, _ = rawModelsGET(t, mux, token, "/v1/models/nope/gpt-5.5"); code != http.StatusNotFound {
		t.Errorf("unknown provider status = %d, want 404", code)
	}
}

// TestModelDetailAcceptsProviderID: the id form is accepted as well as the name.
func TestModelDetailAcceptsProviderID(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newCatalogStore()
	provider := &store.LLMProviderData{
		BaseModel: store.BaseModel{ID: uuid.New()}, Name: "byid", ProviderType: store.ProviderZai,
		APIKey: "k", Enabled: true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	_, mux := newCatalogHandler(t, st)
	code, body := rawModelsGET(t, mux, token, "/v1/models/"+provider.ID.String()+"/glm-5.2")
	if code != http.StatusOK {
		t.Fatalf("status = %d (%v)", code, body)
	}
	if body["provider"] != "byid" {
		t.Errorf("provider = %v", body["provider"])
	}
}
