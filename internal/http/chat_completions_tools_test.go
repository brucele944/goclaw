package http

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// toolCallAgent answers with the tool calls a passthrough caller must execute.
// onRun, when set, runs inside the loop call — the place a real agent loop
// broadcasts its streamed deltas from.
type toolCallAgent struct {
	httpOverrideAgent
	result *agent.RunResult
	onRun  func(agent.RunRequest)
}

func (a *toolCallAgent) Run(ctx context.Context, req agent.RunRequest) (*agent.RunResult, error) {
	if a.onRun != nil {
		a.onRun(req)
	}
	a.requests <- req
	if a.result != nil {
		res := *a.result
		res.RunID = req.RunID
		return &res, nil
	}
	return &agent.RunResult{Content: "answer", RunID: req.RunID, FinishReason: "stop"}, nil
}

func newToolCallHandler(t *testing.T, result *agent.RunResult) (*ChatCompletionsHandler, *toolCallAgent) {
	t.Helper()
	ag := &toolCallAgent{
		httpOverrideAgent: httpOverrideAgent{requests: make(chan agent.RunRequest, 4)},
		result:            result,
	}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewChatCompletionsHandler(router, nil, false)
	return h, ag
}

func postChat(t *testing.T, h *ChatCompletionsHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer http-override-token")
	req.Header.Set("X-GoClaw-User-Id", "user-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestChatCompletionsClientToolsArePassedThrough — caller-declared tools reach the
// run as its tool surface (never executed here), and the model's calls come back
// in OpenAI shape with finish_reason tool_calls.
func TestChatCompletionsClientToolsArePassedThrough(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, &agent.RunResult{
		FinishReason: "tool_calls",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_1",
			Name:      "get_weather",
			Arguments: map[string]any{"city": "Hanoi"},
		}},
	})

	body := `{
		"model": "goclaw:http-agent",
		"messages": [{"role": "user", "content": "weather in hanoi?"}],
		"temperature": 0.2,
		"max_tokens": 128,
		"tool_choice": "required",
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Look up the weather",
				"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
			}
		}]
	}`
	rec := postChat(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var req agent.RunRequest
	select {
	case req = <-ag.requests:
	default:
		t.Fatal("handler returned without running the agent loop")
	}
	if len(req.ClientTools) != 1 || req.ClientTools[0].Function == nil || req.ClientTools[0].Function.Name != "get_weather" {
		t.Fatalf("ClientTools = %+v, want the caller's get_weather definition", req.ClientTools)
	}
	if req.ClientTools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("tool parameters were not carried through: %+v", req.ClientTools[0].Function.Parameters)
	}
	if req.ToolChoice != "required" {
		t.Errorf("ToolChoice = %v, want required", req.ToolChoice)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Errorf("Temperature = %v, want 0.2", req.Temperature)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 128 {
		t.Errorf("MaxTokens = %v, want 128", req.MaxTokens)
	}

	var resp chatCompletionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body %s)", err, rec.Body.String())
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %+v, want one", resp.Choices)
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
	if choice.Message == nil || len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("message tool_calls = %+v, want one call", choice.Message)
	}
	if choice.Message.Content != "" {
		t.Errorf("content = %q, want empty for a tool-calling turn", choice.Message.Content)
	}
	call := choice.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "function" || call.Function.Name != "get_weather" {
		t.Errorf("tool call = %+v", call)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("tool call arguments are not a JSON string: %q (%v)", call.Function.Arguments, err)
	}
	if args["city"] != "Hanoi" {
		t.Errorf("tool call arguments = %v, want city=Hanoi", args)
	}
}

// TestChatCompletionsNamedToolChoiceIsForwarded — the OpenAI object form of
// tool_choice is valid: it reaches the provider verbatim, and only a malformed
// object is rejected.
func TestChatCompletionsNamedToolChoiceIsForwarded(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)

	rec := postChat(t, h, `{
		"model": "goclaw:http-agent",
		"messages": [{"role": "user", "content": "weather in hanoi?"}],
		"tool_choice": {"type": "function", "function": {"name": "get_weather"}},
		"tools": [{"type": "function", "function": {"name": "get_weather"}}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		choice, ok := req.ToolChoice.(map[string]any)
		if !ok {
			t.Fatalf("ToolChoice = %#v, want the forwarded object", req.ToolChoice)
		}
		fn, _ := choice["function"].(map[string]any)
		if choice["type"] != "function" || fn["name"] != "get_weather" {
			t.Fatalf("forwarded tool_choice = %#v, want the caller's named function", choice)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}
}

// registryProvider is a registry entry the handler can resolve by name; only Name
// is read by the override path.
type registryProvider struct{ providers.Provider }

func (registryProvider) Name() string { return "alt-provider" }

// TestChatCompletionsCompositeModelPinsProvider — a `<provider>/<model>` body model
// (the identity the capability DTO hands to the UIs) is a per-request override that
// pins the provider, resolved through the registry.
func TestChatCompletionsCompositeModelPinsProvider(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)
	reg := providers.NewRegistry(func(context.Context) uuid.UUID { return store.MasterTenantID })
	reg.RegisterForTenant(store.MasterTenantID, registryProvider{})
	h.SetProviderRegistry(reg)

	rec := postChat(t, h, `{"model":"alt-provider/composite-model","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.ModelOverride != "composite-model" {
			t.Fatalf("ModelOverride = %q, want the model half of the reference", req.ModelOverride)
		}
		if req.ProviderOverride == nil || req.ProviderOverride.Name() != "alt-provider" {
			t.Fatalf("ProviderOverride = %v, want the provider half resolved through the registry", req.ProviderOverride)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}
}

// TestChatCompletionsUnresolvableModelStillSelectsTheAgent — a `model` value that is
// neither an agent form nor a resolvable `<provider>/<model>` reference keeps its
// long-standing meaning (agent selection): callers that pass a placeholder, as the
// stock-SDK examples and the contract tests do, must not suddenly run on a model
// named after their placeholder.
func TestChatCompletionsUnresolvableModelStillSelectsTheAgent(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)
	reg := providers.NewRegistry(func(context.Context) uuid.UUID { return store.MasterTenantID })
	reg.RegisterForTenant(store.MasterTenantID, registryProvider{})
	h.SetProviderRegistry(reg)

	rec := postChat(t, h, `{"model":"test","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.ModelOverride != "" || req.ProviderOverride != nil {
			t.Fatalf("placeholder model became an override: model=%q provider=%v", req.ModelOverride, req.ProviderOverride)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}

	var resp chatCompletionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Model != "test" {
		t.Fatalf("response model = %q, want the requested %q", resp.Model, "test")
	}
}

// TestChatCompletionsKeepsTextAlongsideClientToolCalls — a tool-calling turn still
// returns the model's text, as OpenAI does; only the calls are withheld from
// server-side execution.
func TestChatCompletionsKeepsTextAlongsideClientToolCalls(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)
	ag.result = &agent.RunResult{
		Content:   "Let me look that up.",
		ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "Hanoi"}}},
	}

	rec := postChat(t, h, `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"weather?"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp chatCompletionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.Message.Content != "Let me look that up." {
		t.Fatalf("content = %q, want the text the model produced with its calls", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 || choice.Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls = %+v, want the client-owned call", choice.Message.ToolCalls)
	}
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
}

// TestChatCompletionsUnsupportedParamsAreRejected — parameters the endpoint cannot
// honour fail loudly instead of silently changing behaviour.
func TestChatCompletionsUnsupportedParamsAreRejected(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)

	cases := []struct {
		name string
		body string
	}{
		{"n>1", `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"}],"n":2}`},
		{"stop", `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"}],"stop":["END"]}`},
		{"bad tool type", `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search"}]}`},
		{"malformed tool_choice object", `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function"}}`},
		{"trailing assistant", `{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hm"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postChat(t, h, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error body is not OpenAI-shaped JSON: %v (%s)", err, rec.Body.String())
			}
			if envelope.Error.Message == "" || envelope.Error.Type != "invalid_request_error" {
				t.Fatalf("error envelope = %+v", envelope.Error)
			}
		})
	}
	select {
	case req := <-ag.requests:
		t.Fatalf("a rejected request still ran the loop: %+v", req)
	default:
	}
}

// TestChatCompletionsToolResultContinuation — a transcript ending in a tool result
// continues the conversation: every message is replayed and no user turn is
// invented, so a tool-calling exchange can round-trip.
func TestChatCompletionsToolResultContinuation(t *testing.T) {
	withGatewayToken(t)
	h, ag := newToolCallHandler(t, nil)

	body := `{
		"model": "goclaw:http-agent",
		"messages": [
			{"role": "user", "content": "weather in hanoi?"},
			{"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Hanoi\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "31C and sunny"}
		]
	}`
	rec := postChat(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.Message != "" {
			t.Fatalf("Message = %q, want empty: a tool result continues the transcript, it is not a user turn", req.Message)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}
}

// TestChatCompletionsSeedsCallerTranscript — prior turns are replayed into the run
// session, so a stateless caller's conversation actually reaches the model.
func TestChatCompletionsSeedsCallerTranscript(t *testing.T) {
	withGatewayToken(t)
	sessions := &recordingSessionStore{}
	ag := &toolCallAgent{httpOverrideAgent: httpOverrideAgent{requests: make(chan agent.RunRequest, 4)}}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })
	h := NewChatCompletionsHandler(router, sessions, false)

	body := `{
		"model": "goclaw:http-agent",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "first"},
			{"role": "assistant", "content": "first answer"},
			{"role": "user", "content": "second"}
		]
	}`
	rec := postChat(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	select {
	case req := <-ag.requests:
		if req.Message != "second" {
			t.Fatalf("Message = %q, want the final user turn", req.Message)
		}
		seeded := sessions.recordedMessages(req.SessionKey)
		if len(seeded) != 3 {
			t.Fatalf("seeded %d messages, want 3 (system, user, assistant): %+v", len(seeded), seeded)
		}
		if seeded[0].Role != "system" || seeded[1].Content != "first" || seeded[2].Content != "first answer" {
			t.Fatalf("seeded transcript = %+v", seeded)
		}
	default:
		t.Fatal("handler returned without running the agent loop")
	}
}

// TestChatCompletionsStreamForwardsDeltasAndFinishReason — a streaming answer
// carries the run's real deltas, ends the turn with the finish reason and closes
// with [DONE]; deltas arrive through the event broadcast the WS clients use.
func TestChatCompletionsStreamForwardsDeltasAndFinishReason(t *testing.T) {
	withGatewayToken(t)
	pub := bus.New()
	h, ag := newToolCallHandler(t, &agent.RunResult{
		Content:      "hello world",
		FinishReason: "stop",
		Usage:        &providers.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
	})
	h.SetEventPublisher(pub)

	// The loop is replaced by the test: emit the same chunk events the agent loop
	// emits for a streamed answer.
	ag.onRun = func(req agent.RunRequest) {
		for _, chunk := range []string{"hello", " world"} {
			pub.Broadcast(bus.Event{
				Name: protocol.EventAgent,
				Payload: agent.AgentEvent{
					Type:    protocol.ChatEventChunk,
					RunID:   req.RunID,
					Payload: map[string]string{"content": chunk},
				},
			})
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"goclaw:http-agent","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`))
	req.Header.Set("Authorization", "Bearer http-override-token")
	req.Header.Set("X-GoClaw-User-Id", "user-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	var (
		content  strings.Builder
		finish   string
		sawDone  bool
		sawUsage bool
		chunkCnt int
	)
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		chunkCnt++
		var chunk struct {
			Choices []struct {
				Delta        *chatMessage `json:"delta"`
				FinishReason *string      `json:"finish_reason"`
			} `json:"choices"`
			Usage *chatUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %v (%s)", err, payload)
		}
		if chunk.Usage != nil {
			sawUsage = true
			if chunk.Usage.PromptTokens != 3 || chunk.Usage.CompletionTokens != 5 || chunk.Usage.TotalTokens != 8 {
				t.Fatalf("usage chunk = %+v, want the run's usage", chunk.Usage)
			}
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if d := chunk.Choices[0].Delta; d != nil {
			content.WriteString(d.Content)
		}
		if chunk.Choices[0].FinishReason != nil {
			finish = *chunk.Choices[0].FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stream: %v", err)
	}
	if got := content.String(); got != "hello world" {
		t.Fatalf("streamed content = %q, want the run's deltas concatenated", got)
	}
	if finish != "stop" {
		t.Fatalf("finish_reason = %q, want stop", finish)
	}
	if !sawDone {
		t.Fatal("stream did not close with [DONE]")
	}
	if !sawUsage {
		t.Fatal("stream_options.include_usage did not produce a usage chunk")
	}
	if chunkCnt == 0 {
		t.Fatal("no chunks were streamed")
	}
}
