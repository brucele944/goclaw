package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// healthStubServer serves the two routes runProvidersHealth reads: the provider
// list and the per-provider health resource. healthStatus lets a test simulate a
// gateway whose binary predates the health route (the real-world 404 case).
func healthStubServer(t *testing.T, healthStatus int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/providers":
			io.WriteString(w, `{"providers":[`+
				`{"id":"11111111-1111-1111-1111-111111111111","name":"alpha"},`+
				`{"id":"22222222-2222-2222-2222-222222222222","name":"beta"}]}`)
		case strings.HasSuffix(r.URL.Path, "/health"):
			if healthStatus != http.StatusOK {
				w.WriteHeader(healthStatus)
				io.WriteString(w, `{"error":"API route not found"}`)
				return
			}
			io.WriteString(w, `{"provider":"alpha","cooling_down":false,"consecutive_failures":0}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	gatewayTokenOverride = "test-token"
	t.Cleanup(func() { gatewayTokenOverride = "" })
	t.Setenv("GOCLAW_GATEWAY_URL", srv.URL)
}

func TestRunProvidersHealthJSONIsAnArray(t *testing.T) {
	healthStubServer(t, http.StatusOK)

	out, err := captureStdout(t, func() error { return runProvidersHealth("", true) })
	if err != nil {
		t.Fatalf("runProvidersHealth: %v", err)
	}

	var reports []map[string]any
	if err := json.Unmarshal([]byte(out), &reports); err != nil {
		t.Fatalf("--json output is not a JSON array: %v (output=%q)", err, out)
	}
	if len(reports) != 2 {
		t.Fatalf("got %d health reports, want 2 (output=%q)", len(reports), out)
	}
}

func TestRunProvidersHealthReportsUnreadableSurface(t *testing.T) {
	healthStubServer(t, http.StatusNotFound)

	out, err := captureStdout(t, func() error { return runProvidersHealth("", true) })
	if err == nil {
		t.Fatal("expected an error when no provider health could be read")
	}
	if !strings.Contains(err.Error(), "all 2 providers failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The flag contract holds even when the surface is unreadable: an array, not
	// null, so a pipeline consuming the output never sees a null document.
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("--json must still print an array, got %q", out)
	}
}
