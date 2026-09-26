package agent

// Tests for the phase-5 capability consumption on the request path: a catalogue
// row's declared capabilities (tool_calling, vision, stream_with_tools,
// cache_control) decide what goes out on the wire.
//
// The fixtures deliberately use a provider NAMED and TYPED something other than
// "dashscope": the decisions must come from the declared row, never from the
// provider's name or Go type.

import (
	"context"
	"strings"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/pipeline"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// catalogueCapabilityProvider is a fake transport whose capabilities come from
// the provider declaration plus the injected catalogue row, and which records
// which transport method the request path chose.
type catalogueCapabilityProvider struct {
	name         string
	providerType string
	caps         providers.ProviderCapabilities

	chatCalls   int
	streamCalls int
	lastRequest providers.ChatRequest
}

func (p *catalogueCapabilityProvider) Chat(_ context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	p.chatCalls++
	p.lastRequest = req
	return &providers.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (p *catalogueCapabilityProvider) ChatStream(_ context.Context, req providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	p.streamCalls++
	p.lastRequest = req
	return &providers.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (p *catalogueCapabilityProvider) DefaultModel() string                         { return "acme-fast-1" }
func (p *catalogueCapabilityProvider) Name() string                                 { return p.name }
func (p *catalogueCapabilityProvider) ProviderType() string                         { return p.providerType }
func (p *catalogueCapabilityProvider) Capabilities() providers.ProviderCapabilities { return p.caps }

// withCatalogueRows installs a fixture model catalogue for one test: keys are
// "providerType/model", values are the overrides llm_models.capabilities would
// carry. Restored on cleanup.
func withCatalogueRows(t *testing.T, rows map[string]providers.ModelCapabilityOverride) {
	t.Helper()
	previous := modelCapabilityLookup
	modelCapabilityLookup = func(_, providerType, model string) (providers.ModelCapabilityOverride, bool) {
		override, ok := rows[providerType+"/"+model]
		return override, ok
	}
	t.Cleanup(func() { modelCapabilityLookup = previous })
}

func capabilityTestLoop(prov providers.Provider, model string) *Loop {
	reg := tools.NewRegistry()
	reg.Register(tools.NewDateTimeTool())
	return &Loop{provider: prov, model: model, tools: reg}
}

// TestToolCallingCapabilityRemovesToolsAndTellsTheModel covers the acceptance
// fixture: capabilities.tool_calling=false on the model's row removes tools from
// the outgoing request and the model is told, in-band, why.
func TestToolCallingCapabilityRemovesToolsAndTellsTheModel(t *testing.T) {
	prov := &catalogueCapabilityProvider{
		name:         "acme-mini",
		providerType: "acme-mini",
		caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true, StreamWithTools: true, Vision: true},
	}
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {ToolCalling: new(false)},
	})
	loop := capabilityTestLoop(prov, "acme-fast-1")

	defs, allowed, messages := loop.buildFilteredTools(&RunRequest{}, false, 1, 10, nil, nil)
	if len(defs) != 0 {
		t.Fatalf("tools sent = %d (%v), want none when the row declares tool_calling=false", len(defs), defs)
	}
	if allowed == nil {
		t.Fatal("allowed-tools map must be non-nil: nil means 'no restriction' to the tool authorizer")
	}
	if len(allowed) != 0 {
		t.Fatalf("allowed tools = %v, want empty so a tool call cannot execute", allowed)
	}
	if len(messages) != 1 {
		t.Fatalf("injected messages = %d, want exactly one notice", len(messages))
	}
	if !strings.Contains(messages[0].Content, "Tools are unavailable for acme-fast-1") {
		t.Errorf("notice = %q, want it to name the model and say tools are unavailable", messages[0].Content)
	}

	// The final iteration must not repeat the notice (the per-run cache replayed
	// it for iterations 0..maxIter-1 already).
	if _, _, finalMsgs := loop.buildFilteredTools(&RunRequest{}, false, 10, 10, nil, nil); len(finalMsgs) != 0 {
		t.Errorf("final-iteration messages = %v, want no duplicate notice", finalMsgs)
	}
}

// TestNoToolCallingRowKeepsTools is the control fixture for the test above: the
// same provider/model pair with no declared override still gets its tools.
func TestNoToolCallingRowKeepsTools(t *testing.T) {
	prov := &catalogueCapabilityProvider{
		name:         "acme-mini",
		providerType: "acme-mini",
		caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true, StreamWithTools: true, Vision: true},
	}
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{})
	loop := capabilityTestLoop(prov, "acme-fast-1")

	defs, _, messages := loop.buildFilteredTools(&RunRequest{}, false, 1, 10, nil, nil)
	if !hasFunctionTool(defs, "datetime") {
		t.Fatalf("tools = %v, want the registry tools when the row declares nothing", defs)
	}
	if len(messages) != 0 {
		t.Errorf("injected messages = %v, want none", messages)
	}
}

// TestToolCallingGatingSkippedWhenProviderUndeclared proves "undeclared" is not
// "unsupported": a transport that does not implement CapabilitiesAware keeps its
// tools even when a row exists.
func TestToolCallingGatingSkippedWhenProviderUndeclared(t *testing.T) {
	prov := &stubProvider{} // does not implement CapabilitiesAware
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {ToolCalling: new(false)},
	})
	loop := capabilityTestLoop(prov, "acme-fast-1")

	defs, _, messages := loop.buildFilteredTools(&RunRequest{}, false, 1, 10, nil, nil)
	if !hasFunctionTool(defs, "datetime") {
		t.Fatalf("tools = %v, want tools kept for a provider that declares no capabilities", defs)
	}
	if len(messages) != 0 {
		t.Errorf("injected messages = %v, want none", messages)
	}
}

// TestToolsStrippedRunStillAnswersWithText proves the gated run does not break
// the loop: with tools gone, the model produces a final text response and the
// iteration breaks normally instead of erroring.
func TestToolsStrippedRunStillAnswersWithText(t *testing.T) {
	prov := &catalogueCapabilityProvider{
		name:         "acme-mini",
		providerType: "acme-mini",
		caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true, Vision: true},
	}
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {ToolCalling: new(false)},
	})
	loop := capabilityTestLoop(prov, "acme-fast-1")
	gatedDefs, _, _ := loop.buildFilteredTools(&RunRequest{}, false, 1, 10, nil, nil)

	deps := &pipeline.PipelineDeps{
		Config: pipeline.PipelineConfig{MaxIterations: 3, MaxTokens: 1000},
		BuildFilteredTools: func(*pipeline.RunState) ([]providers.ToolDefinition, error) {
			return gatedDefs, nil
		},
		CallLLM: func(_ context.Context, _ *pipeline.RunState, req providers.ChatRequest) (*providers.ChatResponse, error) {
			if len(req.Tools) != 0 {
				t.Errorf("outgoing request carried %d tools, want none", len(req.Tools))
			}
			return &providers.ChatResponse{Content: "final text answer", FinishReason: "stop"}, nil
		},
	}
	state := pipeline.NewRunState(&pipeline.RunInput{SessionKey: "sess-1", RunID: "run-1"}, nil, "acme-fast-1", prov)
	stage := pipeline.NewThinkStage(deps)

	if err := stage.Execute(context.Background(), state); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if stage.Result() != pipeline.BreakLoop {
		t.Fatalf("Result() = %v, want BreakLoop after a final text answer", stage.Result())
	}
	if state.Think.LastResponse == nil || state.Think.LastResponse.Content != "final text answer" {
		t.Fatalf("LastResponse = %+v, want the model's final text answer", state.Think.LastResponse)
	}
}

// TestStreamWithToolsIsDataDrivenNotNameSniffed is the regression test for the
// DashScope special case: a provider/model pair named something else entirely,
// with stream_with_tools=false declared on its row, must still take the
// non-stream path — and the same pair streams again when the row says true.
func TestStreamWithToolsIsDataDrivenNotNameSniffed(t *testing.T) {
	newProvider := func() *catalogueCapabilityProvider {
		return &catalogueCapabilityProvider{
			name:         "acme-mini",
			providerType: "acme-mini",
			caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true, StreamWithTools: true, Vision: true},
		}
	}
	runCall := func(t *testing.T, prov *catalogueCapabilityProvider, requested, withTools bool) {
		t.Helper()
		loop := &Loop{id: "acme-agent", provider: prov, model: "acme-fast-1"}
		req := &RunRequest{RunID: "run-1", SessionKey: "sess-1", Stream: requested}
		state := &pipeline.RunState{Provider: prov, Model: "acme-fast-1"}
		chatReq := providers.ChatRequest{Model: "acme-fast-1"}
		if withTools {
			chatReq.Tools = []providers.ToolDefinition{{
				Type:     "function",
				Function: &providers.ToolFunctionSchema{Name: "datetime"},
			}}
		}
		if _, err := loop.makeCallLLM(req, func(AgentEvent) {})(context.Background(), state, chatReq); err != nil {
			t.Fatalf("makeCallLLM error: %v", err)
		}
	}

	// Row declares stream_with_tools=false → tools force the non-stream path.
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {StreamWithTools: new(false)},
	})
	nonStreaming := newProvider()
	runCall(t, nonStreaming, true, true)
	if nonStreaming.chatCalls != 1 || nonStreaming.streamCalls != 0 {
		t.Fatalf("chat=%d stream=%d, want the non-stream path chosen from the row declaration",
			nonStreaming.chatCalls, nonStreaming.streamCalls)
	}

	// A tool-free request from the same model still streams.
	toolFree := newProvider()
	runCall(t, toolFree, true, false)
	if toolFree.streamCalls != 1 || toolFree.chatCalls != 0 {
		t.Fatalf("chat=%d stream=%d, want streaming kept for a request without tools",
			toolFree.chatCalls, toolFree.streamCalls)
	}

	// Row declares stream_with_tools=true → streaming is kept even with tools.
	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {StreamWithTools: new(true)},
	})
	streaming := newProvider()
	runCall(t, streaming, true, true)
	if streaming.streamCalls != 1 || streaming.chatCalls != 0 {
		t.Fatalf("chat=%d stream=%d, want the streaming path when the row declares it",
			streaming.chatCalls, streaming.streamCalls)
	}

	// Requested non-stream is never upgraded to streaming.
	callerChose := newProvider()
	runCall(t, callerChose, false, true)
	if callerChose.chatCalls != 1 || callerChose.streamCalls != 0 {
		t.Fatalf("chat=%d stream=%d, want the caller's non-stream choice honoured",
			callerChose.chatCalls, callerChose.streamCalls)
	}
}

// TestCacheBreakpointParamsComeFromCapabilitiesNotProviderType proves the
// prompt-cache parameters are attached because the model's capabilities declare
// cache_control — for a provider that is neither Codex nor the ChatGPT router,
// which is all the old type switch recognised.
func TestCacheBreakpointParamsComeFromCapabilitiesNotProviderType(t *testing.T) {
	runCall := func(t *testing.T, prov *catalogueCapabilityProvider) providers.ChatRequest {
		t.Helper()
		loop := &Loop{id: "acme-agent", provider: prov, model: "acme-fast-1"}
		req := &RunRequest{RunID: "run-1", SessionKey: "sess-1"}
		state := &pipeline.RunState{Provider: prov, Model: "acme-fast-1"}
		if _, err := loop.makeCallLLM(req, func(AgentEvent) {})(context.Background(), state, providers.ChatRequest{Model: "acme-fast-1"}); err != nil {
			t.Fatalf("makeCallLLM error: %v", err)
		}
		return prov.lastRequest
	}

	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{
		"acme-mini/acme-fast-1": {CacheControl: new(true)},
	})
	withCache := &catalogueCapabilityProvider{
		name:         "acme-mini",
		providerType: "acme-mini",
		caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true, CacheControl: false},
	}
	if got := runCall(t, withCache); got.Options[providers.OptPromptCacheKey] == nil {
		t.Errorf("options = %v, want prompt_cache_key set from the row's cache_control=true", got.Options)
	}

	withCatalogueRows(t, map[string]providers.ModelCapabilityOverride{})
	withoutCache := &catalogueCapabilityProvider{
		name:         "acme-mini",
		providerType: "acme-mini",
		caps:         providers.ProviderCapabilities{Streaming: true, ToolCalling: true},
	}
	if got := runCall(t, withoutCache); got.Options[providers.OptPromptCacheKey] != nil {
		t.Errorf("options = %v, want no prompt_cache_key when nothing declares cache support", got.Options)
	}
}
