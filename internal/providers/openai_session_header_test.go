package providers

// Coverage for the per-conversation routing header. OpenCode Go rejects requests
// that arrive without x-opencode-session ("cannot be routed efficiently"), so the
// value must be present, derived from the run's session key, and stable for the
// whole conversation.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sessionHeaderName = "x-opencode-session"

// newSessionHeaderServer records the header value of every request it serves.
func newSessionHeaderServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(sessionHeaderName))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func chatWithSessionKey(t *testing.T, p *OpenAIProvider, key string) {
	t.Helper()
	opts := map[string]any{}
	if key != "" {
		opts[OptSessionKey] = key
	}
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Options:  opts,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want ok", resp.Content)
	}
}

func TestOpenAIProvider_SessionHeaderIsStablePerConversation(t *testing.T) {
	srv, seen := newSessionHeaderServer(t)
	p := NewOpenAIProvider("opencode-go-test", "sk-fake", srv.URL, "deepseek-v4.1-flash").
		WithSessionHeader(sessionHeaderName)

	const key = "agent:little-fox:telegram:direct:690956081"
	chatWithSessionKey(t, p, key)
	chatWithSessionKey(t, p, key)
	chatWithSessionKey(t, p, "agent:little-fox:ws:direct:other")

	if len(*seen) != 3 {
		t.Fatalf("requests = %d, want 3", len(*seen))
	}
	want := deriveSessionUUID(key).String()
	if (*seen)[0] != want || (*seen)[1] != want {
		t.Errorf("session ids = %q, %q; want the derived id %q for both turns of one conversation",
			(*seen)[0], (*seen)[1], want)
	}
	if (*seen)[2] == want {
		t.Errorf("a different session key produced the same id %q", want)
	}
	if (*seen)[2] == "" {
		t.Error("session id must never be empty")
	}
}

// A call without a session (capability probe, auxiliary turn) still sends an id —
// a missing header is what the gateway complains about — but it must not be
// presented as a stable conversation id shared across callers.
func TestOpenAIProvider_SessionHeaderWithoutSessionKeyStillSendsID(t *testing.T) {
	srv, seen := newSessionHeaderServer(t)
	p := NewOpenAIProvider("opencode-go-test", "sk-fake", srv.URL, "deepseek-v4.1-flash").
		WithSessionHeader(sessionHeaderName)

	chatWithSessionKey(t, p, "")
	chatWithSessionKey(t, p, "")

	if len(*seen) != 2 {
		t.Fatalf("requests = %d, want 2", len(*seen))
	}
	if (*seen)[0] == "" || (*seen)[1] == "" {
		t.Fatalf("session ids = %q, %q; want a generated id on both", (*seen)[0], (*seen)[1])
	}
	if (*seen)[0] == (*seen)[1] {
		t.Errorf("session-less calls reused the id %q; want one-off ids", (*seen)[0])
	}
}

func TestOpenAIProvider_SessionHeaderAbsentWhenUnconfigured(t *testing.T) {
	srv, seen := newSessionHeaderServer(t)
	p := NewOpenAIProvider("plain-openai-test", "sk-fake", srv.URL, "gpt-4o")

	chatWithSessionKey(t, p, "agent:x:ws:direct:1")

	if len(*seen) != 1 || (*seen)[0] != "" {
		t.Fatalf("session header = %q; want none for a provider that declares no session header", (*seen)[0])
	}
}

// doRequest is the single choke point: a caller that stamped the context directly
// gets exactly that value (used by the model-listing path, which has no chat turn).
func TestOpenAIProvider_SessionHeaderFromContext(t *testing.T) {
	srv, seen := newSessionHeaderServer(t)
	p := NewOpenAIProvider("opencode-go-test", "sk-fake", srv.URL, "deepseek-v4.1-flash").
		WithSessionHeader(sessionHeaderName)

	body, err := p.doRequest(WithProviderSessionID(context.Background(), "listing-42"), map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("doRequest: %v", err)
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()

	if len(*seen) != 1 || (*seen)[0] != "listing-42" {
		t.Fatalf("session header = %q; want the stamped context value", (*seen)[0])
	}
}
