package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// httpOverrideProvider satisfies providers.Provider for the agent returned below.
type httpOverrideProvider struct{}

func (httpOverrideProvider) Chat(context.Context, providers.ChatRequest) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{Content: "ok"}, nil
}

func (httpOverrideProvider) ChatStream(context.Context, providers.ChatRequest, func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{Content: "ok"}, nil
}

func (httpOverrideProvider) DefaultModel() string { return "agent-default" }
func (httpOverrideProvider) Name() string         { return "http-provider" }

// httpOverrideAgent records the RunRequest the HTTP handler sends to the loop.
type httpOverrideAgent struct {
	requests chan agent.RunRequest
}

func (a *httpOverrideAgent) ID() string                   { return "http-agent" }
func (a *httpOverrideAgent) UUID() uuid.UUID              { return uuid.Nil }
func (a *httpOverrideAgent) OtherConfig() json.RawMessage { return nil }
func (a *httpOverrideAgent) IsRunning() bool              { return false }
func (a *httpOverrideAgent) Model() string                { return "agent-model" }
func (a *httpOverrideAgent) ProviderName() string         { return "http-provider" }
func (a *httpOverrideAgent) Provider() providers.Provider { return httpOverrideProvider{} }

func (a *httpOverrideAgent) Run(_ context.Context, req agent.RunRequest) (*agent.RunResult, error) {
	a.requests <- req
	return &agent.RunResult{Content: "answer", RunID: req.RunID}, nil
}

func newChatCompletionsOverrideHandler(t *testing.T) (*ChatCompletionsHandler, *httpOverrideAgent) {
	t.Helper()
	ag := &httpOverrideAgent{requests: make(chan agent.RunRequest, 4)}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewChatCompletionsHandler(router, nil, false)
	return h, ag
}

func authenticatedChatRequest(t *testing.T, model, headerModel string) *http.Request {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer http-override-token")
	req.Header.Set("X-GoClaw-User-Id", "user-1")
	if headerModel != "" {
		req.Header.Set("X-GoClaw-Model", headerModel)
	}
	return req
}

func withGatewayToken(t *testing.T) {
	t.Helper()
	old := pkgGatewayToken
	pkgGatewayToken = "http-override-token"
	t.Cleanup(func() { pkgGatewayToken = old })
}

// TestChatCompletionsModelHeaderOverride — X-GoClaw-Model overrides the model of
// the run (the body `model` field only selects the agent), and the response
// reports the effective model.
func TestChatCompletionsModelHeaderOverride(t *testing.T) {
	withGatewayToken(t)
	h, ag := newChatCompletionsOverrideHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedChatRequest(t, "goclaw:http-agent", "header-model"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.ModelOverride != "header-model" {
			t.Fatalf("ModelOverride = %q, want %q", req.ModelOverride, "header-model")
		}
		if req.ProviderOverride != nil {
			t.Fatalf("ProviderOverride = %v, want nil (header only pins the model)", req.ProviderOverride)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}

	var resp chatCompletionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body %s)", err, rec.Body.String())
	}
	if resp.Model != "header-model" {
		t.Fatalf("response model = %q, want the effective override %q", resp.Model, "header-model")
	}
}

// TestChatCompletionsWithoutModelHeaderKeepsAgentModel — no header means no
// override: the run keeps the agent's own model selection.
func TestChatCompletionsWithoutModelHeaderKeepsAgentModel(t *testing.T) {
	withGatewayToken(t)
	h, ag := newChatCompletionsOverrideHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedChatRequest(t, "goclaw:http-agent", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.ModelOverride != "" {
			t.Fatalf("ModelOverride = %q, want empty when no header is sent", req.ModelOverride)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}

	// The body `model` (agent selector) is still echoed back, as before.
	var resp chatCompletionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Model != "goclaw:http-agent" {
		t.Fatalf("response model = %q, want the requested %q", resp.Model, "goclaw:http-agent")
	}
}
