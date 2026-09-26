package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// newProviderHealthHandler builds a ProvidersHandler backed by the in-memory
// provider mock (which keeps provider_health state) and registers its routes.
func newProviderHealthHandler(t *testing.T, s store.ProviderStore) (*http.ServeMux, *ProvidersHandler) {
	t.Helper()
	handler := NewProvidersHandler(s, newMockSecretsStore(), nil, "")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux, handler
}

func providerHealthRequest(t *testing.T, mux *http.ServeMux, method, token, providerID string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/v1/providers/"+providerID+"/health", reader)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var decoded map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w.Code, decoded
}

// TestProviderHealthReportsDurableState: GET reports the persisted cooldown,
// failure count, error-class histogram and the cooldown ceiling the runtime
// enforces.
func TestProviderHealthReportsDurableState(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newMockProviderStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "health-target",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}

	// A fresh provider has no row: healthy by definition.
	mux, _ := newProviderHealthHandler(t, st)
	status, body := providerHealthRequest(t, mux, http.MethodGet, token, provider.ID.String(), nil)
	if status != http.StatusOK {
		t.Fatalf("GET health status = %d, body = %v", status, body)
	}
	if cooling, _ := body["cooling_down"].(bool); cooling {
		t.Errorf("fresh provider reports cooling_down: %v", body)
	}
	if failures, _ := body["consecutive_failures"].(float64); failures != 0 {
		t.Errorf("fresh provider consecutive_failures = %v, want 0", body["consecutive_failures"])
	}

	// Record a failure the way the fallback wrapper would.
	deadline := time.Now().UTC().Add(30 * time.Second).Truncate(time.Millisecond)
	if err := st.RecordProviderFailure(t.Context(), provider.ID, "rate_limit", deadline); err != nil {
		t.Fatal(err)
	}

	status, body = providerHealthRequest(t, mux, http.MethodGet, token, provider.ID.String(), nil)
	if status != http.StatusOK {
		t.Fatalf("GET health status = %d, body = %v", status, body)
	}
	if cooling, _ := body["cooling_down"].(bool); !cooling {
		t.Errorf("cooling_down = false after a recorded failure: %v", body)
	}
	if failures, _ := body["consecutive_failures"].(float64); failures != 1 {
		t.Errorf("consecutive_failures = %v, want 1", body["consecutive_failures"])
	}
	if class, _ := body["last_error_class"].(string); class != "rate_limit" {
		t.Errorf("last_error_class = %q, want rate_limit", class)
	}
	if until, _ := body["cooldown_until"].(string); until == "" {
		t.Errorf("cooldown_until missing: %v", body)
	}
	counts, _ := body["error_counts"].(map[string]any)
	if counts["rate_limit"] != float64(1) {
		t.Errorf("error_counts = %v, want rate_limit=1", body["error_counts"])
	}
	if maxSeconds, _ := body["max_cooldown_seconds"].(float64); int(maxSeconds) == 0 {
		t.Errorf("max_cooldown_seconds missing: %v", body)
	}
}

// TestProviderHealthResetClearsCooldown is the CLI's `providers health --reset`
// path at the HTTP layer: the manual escape hatch for a cooldown that outlived
// the outage that caused it.
func TestProviderHealthResetClearsCooldown(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newMockProviderStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "reset-target",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordProviderFailure(t.Context(), provider.ID, "auth", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mux, _ := newProviderHealthHandler(t, st)

	status, body := providerHealthRequest(t, mux, http.MethodPost, token, provider.ID.String(), map[string]any{"reset": true})
	if status != http.StatusOK {
		t.Fatalf("reset status = %d, body = %v", status, body)
	}
	if cooling, _ := body["cooling_down"].(bool); cooling {
		t.Errorf("cooling_down = true after reset: %v", body)
	}
	if failures, _ := body["consecutive_failures"].(float64); failures != 0 {
		t.Errorf("consecutive_failures = %v after reset, want 0", body["consecutive_failures"])
	}

	health, err := st.GetProviderHealth(t.Context(), provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth: %v", err)
	}
	if health.CooldownUntil != nil || health.ConsecutiveFailures != 0 || len(health.ErrorCounts) != 0 {
		t.Errorf("store still holds health after reset: %+v", health)
	}
}

// TestProviderHealthRejectsEmptyAction and the method guard: mutations need an
// explicit action, and the resource only answers GET/POST.
func TestProviderHealthRejectsEmptyAction(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newMockProviderStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "noop-target",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	mux, _ := newProviderHealthHandler(t, st)

	status, _ := providerHealthRequest(t, mux, http.MethodPost, token, provider.ID.String(), map[string]any{})
	if status != http.StatusBadRequest {
		t.Errorf("POST with no action status = %d, want 400", status)
	}

	status, _ = providerHealthRequest(t, mux, http.MethodDelete, token, provider.ID.String(), nil)
	if status != http.StatusMethodNotAllowed {
		t.Errorf("DELETE status = %d, want 405", status)
	}

	status, _ = providerHealthRequest(t, mux, http.MethodGet, token, uuid.New().String(), nil)
	if status != http.StatusNotFound {
		t.Errorf("GET for an unknown provider status = %d, want 404", status)
	}
}

// TestProviderHealthProbeRecordsOutcome: the active probe is explicitly
// triggered, records its outcome in provider_health, and a failure starts a
// cooldown with the runtime's duration rules. The probe goes through the shared
// verify handler, so no provider registry means the probe reports the transport
// failure instead of calling an LLM.
func TestProviderHealthProbeRecordsOutcome(t *testing.T) {
	token := setupProvidersAdminToken(t)
	st := newMockProviderStore()
	provider := &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "probe-target",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	mux, _ := newProviderHealthHandler(t, st)

	status, body := providerHealthRequest(t, mux, http.MethodPost, token, provider.ID.String(), map[string]any{"probe": true})
	if status != http.StatusOK {
		t.Fatalf("probe status = %d, body = %v", status, body)
	}
	probe, ok := body["probe"].(map[string]any)
	if !ok {
		t.Fatalf("probe result missing: %v", body)
	}
	if valid, _ := probe["valid"].(bool); valid {
		t.Errorf("probe reported valid with no provider registry: %v", probe)
	}
	if mode, _ := probe["mode"].(string); mode != "reachability" {
		t.Errorf("probe mode = %q, want reachability (no model could be resolved)", mode)
	}
	if class, _ := probe["error_class"].(string); class == "" {
		t.Errorf("probe error_class missing: %v", probe)
	}
	if cooling, _ := body["cooling_down"].(bool); !cooling {
		t.Errorf("a failed probe must start a cooldown: %v", body)
	}
	if at, _ := body["last_probe_at"].(string); at == "" {
		t.Errorf("last_probe_at missing after a probe: %v", body)
	}

	health, err := st.GetProviderHealth(t.Context(), provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth: %v", err)
	}
	if health.ConsecutiveFailures != 1 || health.CooldownUntil == nil || health.LastProbeAt == nil {
		t.Errorf("health after a failed probe = %+v, want 1 failure with a cooldown and a probe stamp", health)
	}
	if health.CooldownUntil.After(time.Now().UTC().Add(providers.MaxCooldown + time.Minute)) {
		t.Errorf("probe cooldown = %v, beyond the runtime ceiling", health.CooldownUntil)
	}
}
