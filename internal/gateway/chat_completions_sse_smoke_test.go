package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	httpapi "github.com/nextlevelbuilder/goclaw/internal/http"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// sseSmokeAgent stands in for an agent loop: it broadcasts the same chunk events a
// real Loop emits while streaming, on the bus the server is wired to.
type sseSmokeAgent struct {
	events bus.EventPublisher
}

func (a *sseSmokeAgent) ID() string                   { return "sse-agent" }
func (a *sseSmokeAgent) UUID() uuid.UUID              { return uuid.Nil }
func (a *sseSmokeAgent) OtherConfig() json.RawMessage { return nil }
func (a *sseSmokeAgent) IsRunning() bool              { return false }
func (a *sseSmokeAgent) Model() string                { return "sse-model" }
func (a *sseSmokeAgent) ProviderName() string         { return "sse-provider" }
func (a *sseSmokeAgent) Provider() providers.Provider { return nil }

func (a *sseSmokeAgent) Run(_ context.Context, req agent.RunRequest) (*agent.RunResult, error) {
	for _, chunk := range []string{"stream", "ed over", " the wire"} {
		a.events.Broadcast(bus.Event{
			Name: protocol.EventAgent,
			Payload: agent.AgentEvent{
				Type:    protocol.ChatEventChunk,
				AgentID: a.ID(),
				RunID:   req.RunID,
				Payload: map[string]string{"content": chunk},
			},
		})
	}
	return &agent.RunResult{
		Content:      "streamed over the wire",
		FinishReason: "stop",
		RunID:        req.RunID,
		Usage:        &providers.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6},
	}, nil
}

// waitForTestServer polls /health until the listener accepts connections.
func waitForTestServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("test server at %s never became ready", addr)
}

// TestChatCompletionsSSEOverRealServer exercises the production route on a real
// socket: the event publisher the server wires into the handler is the same bus the
// agent loop broadcasts on, so streamed responses arrive as deltas rather than one
// buffered chunk.
func TestChatCompletionsSSEOverRealServer(t *testing.T) {
	httpapi.InitGatewayToken("sse-smoke-token")

	msgBus := bus.New()
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) {
		return &sseSmokeAgent{events: msgBus}, nil
	})
	srv := NewServer(&config.Config{}, msgBus, router, nil)
	srv.SetProviderRegistry(providers.NewRegistry(func(context.Context) uuid.UUID { return store.MasterTenantID }))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, start := StartTestServer(srv, ctx)
	go start() // Serve blocks; ctx cancellation shuts the listener down
	waitForTestServer(t, addr)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"goclaw:sse-agent","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sse-smoke-token")
	req.Header.Set("X-GoClaw-User-Id", "user-1")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	var (
		content     strings.Builder
		finish      string
		sawDone     bool
		sawUsage    bool
		deltaChunks int
	)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta        *struct{ Content string } `json:"delta"`
				FinishReason *string                   `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %v (%s)", err, payload)
		}
		if chunk.Usage != nil {
			sawUsage = true
			if chunk.Usage.TotalTokens != 6 {
				t.Errorf("usage total = %d, want 6 (payload %s)", chunk.Usage.TotalTokens, payload)
			}
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if d := chunk.Choices[0].Delta; d != nil && d.Content != "" {
			deltaChunks++
			content.WriteString(d.Content)
		}
		if chunk.Choices[0].FinishReason != nil {
			finish = *chunk.Choices[0].FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if got := content.String(); got != "streamed over the wire" {
		t.Fatalf("streamed content = %q, want the broadcast deltas", got)
	}
	if deltaChunks < 3 {
		t.Fatalf("content arrived in %d delta chunk(s), want one per broadcast (streaming, not buffered)", deltaChunks)
	}
	if finish != "stop" {
		t.Fatalf("finish_reason = %q, want stop", finish)
	}
	if !sawUsage {
		t.Fatal("include_usage produced no usage chunk")
	}
	if !sawDone {
		t.Fatal("stream did not close with [DONE]")
	}
}
