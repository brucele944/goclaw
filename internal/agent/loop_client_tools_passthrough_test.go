package agent

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// toolCallingProvider answers every request with a tool call, recording what the
// run actually dispatched.
type toolCallingProvider struct {
	name     string
	requests []providers.ChatRequest
	response *providers.ChatResponse
}

func (p *toolCallingProvider) Chat(_ context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.response != nil {
		return p.response, nil
	}
	return &providers.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_1",
			Name:      "get_weather",
			Arguments: map[string]any{"city": "Hanoi"},
		}},
		Usage: &providers.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}, nil
}

func (p *toolCallingProvider) ChatStream(ctx context.Context, req providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func (p *toolCallingProvider) DefaultModel() string { return p.name + "-default" }
func (p *toolCallingProvider) Name() string         { return p.name }

func newClientToolLoop(t *testing.T, provider providers.Provider, sess *nopSessionStore) *Loop {
	t.Helper()
	ws := t.TempDir()
	return NewLoop(LoopConfig{
		ID:            "smoke-agent",
		Provider:      provider,
		Model:         "smoke-model",
		ContextWindow: 128_000,
		MaxIterations: 3,
		Workspace:     ws,
		DataDir:       ws,
		Tools:         tools.NewRegistry(),
		Sessions:      sess,
	})
}

func weatherToolDef() []providers.ToolDefinition {
	return []providers.ToolDefinition{{
		Type:     "function",
		Function: &providers.ToolFunctionSchema{Name: "get_weather"},
	}}
}

// TestRunClientToolsPassthroughEndToEnd — a run whose tools belong to the caller
// offers exactly those definitions to the provider, honours the per-request
// generation options, and returns the model's tool calls to the caller instead of
// executing them (no second iteration).
func TestRunClientToolsPassthroughEndToEnd(t *testing.T) {
	prov := &toolCallingProvider{name: "smoke-provider"}
	sess := &nopSessionStore{}
	loop := newClientToolLoop(t, prov, sess)

	temperature, maxTokens := 0.25, 77
	result, err := loop.Run(context.Background(), RunRequest{
		SessionKey:  "agent:smoke-agent:test",
		RunID:       "run-client-tools",
		Message:     "weather in hanoi?",
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
		ToolChoice:  "required",
		ClientTools: weatherToolDef(),
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1: the run must end at the caller's tool call", len(prov.requests))
	}
	chatReq := prov.requests[0]
	if len(chatReq.Tools) != 1 || chatReq.Tools[0].Function == nil || chatReq.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("provider tools = %+v, want exactly the caller's get_weather", chatReq.Tools)
	}
	if got := chatReq.Options[providers.OptTemperature]; got != 0.25 {
		t.Errorf("temperature option = %v, want 0.25", got)
	}
	if got := chatReq.Options[providers.OptMaxTokens]; got != 77 {
		t.Errorf("max_tokens option = %v, want 77", got)
	}
	if got := chatReq.Options[providers.OptToolChoice]; got != "required" {
		t.Errorf("tool_choice option = %v, want required", got)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("result tool calls = %+v, want the model's single call", result.ToolCalls)
	}
	if result.ToolCalls[0].Name != "get_weather" || result.ToolCalls[0].ID != "call_1" {
		t.Errorf("result tool call = %+v", result.ToolCalls[0])
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", result.FinishReason)
	}
	if result.Content != "" {
		t.Errorf("content = %q, want empty for a tool-calling turn", result.Content)
	}
}

// TestRunToolResultContinuationHasNoFabricatedUserTurn — a stateless caller feeding
// tool results back runs on the transcript it supplied: the provider sees the
// conversation ending in the tool result and no empty user turn is invented.
func TestRunToolResultContinuationHasNoFabricatedUserTurn(t *testing.T) {
	prov := &toolCallingProvider{
		name:     "smoke-provider",
		response: &providers.ChatResponse{Content: "31C and sunny", FinishReason: "stop"},
	}
	sess := &nopSessionStore{history: []providers.Message{
		{Role: "user", Content: "weather in hanoi?"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{
			ID:        "call_1",
			Name:      "get_weather",
			Arguments: map[string]any{"city": "Hanoi"},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: "31C and sunny"},
	}}
	loop := newClientToolLoop(t, prov, sess)

	result, err := loop.Run(context.Background(), RunRequest{
		SessionKey:  "agent:smoke-agent:test",
		RunID:       "run-continuation",
		Message:     "", // no new user turn: the caller is feeding tool results back
		ClientTools: weatherToolDef(),
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Content != "31C and sunny" {
		t.Fatalf("content = %q, want the model's answer to the tool result", result.Content)
	}

	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	msgs := prov.requests[0].Messages
	if len(msgs) == 0 {
		t.Fatal("provider received no messages")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" {
		t.Fatalf("last message = %+v, want the tool result (no fabricated user turn)", last)
	}
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "" {
			t.Fatalf("an empty user turn was fabricated: %+v", msgs)
		}
	}

	// The persisted turn records which model answered, in the qualified identity
	// chat.history reports to the UIs.
	var assistant *providers.Message
	for i := range sess.recorded {
		if sess.recorded[i].Role == "assistant" {
			assistant = &sess.recorded[i]
		}
	}
	if assistant == nil {
		t.Fatalf("no assistant turn was persisted: %+v", sess.recorded)
	}
	if assistant.Model != "smoke-provider/smoke-model" || assistant.Provider != "smoke-provider" {
		t.Errorf("persisted assistant identity = (%q, %q), want (smoke-provider/smoke-model, smoke-provider)",
			assistant.Model, assistant.Provider)
	}
	for _, m := range sess.recorded {
		if m.Role == "user" {
			t.Errorf("a user turn was persisted for a continuation run: %+v", m)
		}
	}
}

// countingTool is a registry tool that records whether a run executed it.
type countingTool struct{ calls int }

func (c *countingTool) Name() string               { return "exec" }
func (c *countingTool) Description() string        { return "test tool" }
func (c *countingTool) Parameters() map[string]any { return nil }
func (c *countingTool) Execute(context.Context, map[string]any) *tools.Result {
	c.calls++
	return &tools.Result{ForLLM: "executed server-side"}
}

// TestRunClientToolsAreNeverExecutedServerSide — a caller-owned tool whose name
// collides with a real agent tool must still be handed back, never run here: the
// caller owns that call and executes it.
func TestRunClientToolsAreNeverExecutedServerSide(t *testing.T) {
	prov := &toolCallingProvider{
		name: "smoke-provider",
		response: &providers.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_exec",
				Name:      "exec",
				Arguments: map[string]any{"command": "echo hi"},
			}},
		},
	}
	sess := &nopSessionStore{}
	registry := tools.NewRegistry()
	tool := &countingTool{}
	registry.Register(tool)
	ws := t.TempDir()
	loop := NewLoop(LoopConfig{
		ID:            "smoke-agent",
		Provider:      prov,
		Model:         "smoke-model",
		ContextWindow: 128_000,
		MaxIterations: 3,
		Workspace:     ws,
		DataDir:       ws,
		Tools:         registry,
		Sessions:      sess,
	})

	result, err := loop.Run(context.Background(), RunRequest{
		SessionKey: "agent:smoke-agent:test",
		RunID:      "run-collision",
		Message:    "do it",
		ClientTools: []providers.ToolDefinition{{
			Type:     "function",
			Function: &providers.ToolFunctionSchema{Name: "exec"},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if tool.calls != 0 {
		t.Fatalf("the agent executed the caller's tool %d time(s): caller-owned calls must never run server-side", tool.calls)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "exec" {
		t.Fatalf("result tool calls = %+v, want the caller's exec call handed back", result.ToolCalls)
	}
	for _, m := range sess.recorded {
		if m.Role == "tool" {
			t.Fatalf("a tool result for a caller-owned call was persisted: %+v", m)
		}
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
}

// TestRunNamedToolChoiceReachesProviderRequest — the OpenAI object form of
// tool_choice is forwarded to the provider request unchanged, so naming a function
// works the same way it does against OpenAI.
func TestRunNamedToolChoiceReachesProviderRequest(t *testing.T) {
	prov := &toolCallingProvider{
		name:     "smoke-provider",
		response: &providers.ChatResponse{Content: "done", FinishReason: "stop"},
	}
	sess := &nopSessionStore{}
	loop := newClientToolLoop(t, prov, sess)

	named := map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}
	if _, err := loop.Run(context.Background(), RunRequest{
		SessionKey:  "agent:smoke-agent:test",
		RunID:       "run-named-choice",
		Message:     "weather?",
		ToolChoice:  named,
		ClientTools: weatherToolDef(),
	}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(prov.requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.requests))
	}
	got := prov.requests[0].Options[providers.OptToolChoice]
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("provider tool_choice option = %#v, want the object form", got)
	}
	fn, _ := obj["function"].(map[string]any)
	if obj["type"] != "function" || fn["name"] != "get_weather" {
		t.Fatalf("provider tool_choice option = %#v, want the caller's named function", obj)
	}
}
