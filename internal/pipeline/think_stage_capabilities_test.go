package pipeline

// Tests for the phase-5 capability consumption in ThinkStage: the model's
// declared capabilities decide whether images go out on the wire and how large
// the request budget may be.

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// capabilityRunState builds a run state whose conversation carries one image —
// the input the vision gate has to act on.
func capabilityRunState(iteration int) *RunState {
	state := defaultState()
	state.Iteration = iteration
	state.Messages.SetHistory([]providers.Message{{
		Role:    "user",
		Content: "what is in this picture?",
		Images:  []providers.ImageContent{{MimeType: "image/png", Data: "aGk="}},
	}})
	return state
}

// TestVisionCapabilityStripsImagesWithNoticeAndKeepsThemOtherwise is the
// two-fixture acceptance test: a row declaring vision=false drops the image
// blocks from the outgoing request and tells the user; a vision-capable model
// (and an undeclared one) keeps them.
func TestVisionCapabilityStripsImagesWithNoticeAndKeepsThemOtherwise(t *testing.T) {
	t.Parallel()

	runWith := func(t *testing.T, resolution func() providers.ModelCapabilityResolution, iteration int) (providers.ChatRequest, []string) {
		t.Helper()
		var notices []string
		var sent providers.ChatRequest
		deps := &PipelineDeps{
			Config:            PipelineConfig{MaxIterations: 3, MaxTokens: 1000},
			ModelCapabilities: resolution,
			EmitBlockReply:    func(content string) { notices = append(notices, content) },
			CallLLM: func(_ context.Context, _ *RunState, req providers.ChatRequest) (*providers.ChatResponse, error) {
				sent = req
				return &providers.ChatResponse{Content: "a cat", FinishReason: "stop"}, nil
			},
		}
		state := capabilityRunState(iteration)
		if err := NewThinkStage(deps).Execute(context.Background(), state); err != nil {
			t.Fatalf("Execute() error: %v", err)
		}
		return sent, notices
	}

	visionOff := func() providers.ModelCapabilityResolution {
		return providers.ModelCapabilityResolution{
			ProviderDeclared: true,
			Capabilities:     providers.ProviderCapabilities{Streaming: true, ToolCalling: true, Vision: false},
		}
	}
	visionOn := func() providers.ModelCapabilityResolution {
		return providers.ModelCapabilityResolution{
			ProviderDeclared: true,
			Capabilities:     providers.ProviderCapabilities{Streaming: true, ToolCalling: true, Vision: true},
		}
	}

	// Fixture 1: row declares vision=false → images stripped, user told.
	sent, notices := runWith(t, visionOff, 0)
	if hasImageBlocks(sent.Messages) {
		t.Errorf("outgoing messages still carry images: %+v", sent.Messages)
	}
	want := i18n.T(store.LocaleFromContext(context.Background()), i18n.MsgModelWithoutVisionNotice)
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("notices = %q, want exactly [%q]", notices, want)
	}

	// Fixture 2: row declares vision=true → images kept, no notice.
	sent, notices = runWith(t, visionOn, 0)
	if !hasImageBlocks(sent.Messages) {
		t.Errorf("outgoing messages lost the images although the model is vision-capable: %+v", sent.Messages)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none for a vision-capable model", notices)
	}

	// Fixture 3: no declaration at all (callback nil) → no gating, images kept.
	sent, notices = runWith(t, nil, 0)
	if !hasImageBlocks(sent.Messages) {
		t.Errorf("outgoing messages lost the images without any declaration: %+v", sent.Messages)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none without a declaration", notices)
	}

	// Later iterations still strip, but do not repeat the user-visible notice.
	sent, notices = runWith(t, visionOff, 2)
	if hasImageBlocks(sent.Messages) {
		t.Errorf("iteration 2 still sent images to a model without vision")
	}
	if len(notices) != 0 {
		t.Errorf("notices = %q, want the notice only on the first iteration", notices)
	}
}

// TestVisionStripKeepsConversationIntact verifies the gate works on the outgoing
// copy only: the conversation buffer keeps the images, so persisted history (and
// a later run against a vision model) still has them.
func TestVisionStripKeepsConversationIntact(t *testing.T) {
	t.Parallel()
	deps := &PipelineDeps{
		Config: PipelineConfig{MaxIterations: 3, MaxTokens: 1000},
		ModelCapabilities: func() providers.ModelCapabilityResolution {
			return providers.ModelCapabilityResolution{
				ProviderDeclared: true,
				Capabilities:     providers.ProviderCapabilities{Vision: false},
			}
		},
		CallLLM: func(_ context.Context, _ *RunState, _ providers.ChatRequest) (*providers.ChatResponse, error) {
			return &providers.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
		},
	}
	state := capabilityRunState(0)
	if err := NewThinkStage(deps).Execute(context.Background(), state); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !hasImageBlocks(state.Messages.All()) {
		t.Fatal("the conversation buffer lost the user's images — they must survive the strip")
	}
}

// TestRequestBudgetClampsToCataloguedModelWindow covers the MaxContextWindow
// clamp: a row that declares the model's window lowers the request budget,
// never raises it, and an undeclared row leaves the agent's window alone.
func TestRequestBudgetClampsToCataloguedModelWindow(t *testing.T) {
	t.Parallel()

	estimateFor := func(t *testing.T, clamp int, declared bool, agentWindow int) FinalRequestEstimate {
		t.Helper()
		deps := &PipelineDeps{
			Config: PipelineConfig{MaxTokens: 512},
			ModelCapabilities: func() providers.ModelCapabilityResolution {
				return providers.ModelCapabilityResolution{ProviderDeclared: declared, ContextWindowClamp: clamp}
			},
		}
		state := defaultState()
		state.Context.EffectiveContextWindow = agentWindow
		estimate, err := NewThinkStage(deps).finalRequestEstimate(state, providers.ChatRequest{
			Messages: []providers.Message{{Role: "user", Content: "hello"}},
		})
		if err != nil {
			t.Fatalf("finalRequestEstimate() error: %v", err)
		}
		return estimate
	}

	clamped := estimateFor(t, 8_000, true, 200_000)
	if clamped.ContextWindow != 8_000 {
		t.Errorf("ContextWindow = %d, want the catalogue clamp 8000", clamped.ContextWindow)
	}
	if clamped.HardInputCapTokens != 8_000-512 {
		t.Errorf("HardInputCapTokens = %d, want window-output_reserve = %d", clamped.HardInputCapTokens, 8_000-512)
	}

	// A window larger than the agent's configuration is not a clamp: raising the
	// budget from a catalogue row would silently change every existing agent.
	raised := estimateFor(t, 500_000, true, 200_000)
	if raised.ContextWindow != 200_000 {
		t.Errorf("ContextWindow = %d, want the agent window 200000 (a row must never raise it)", raised.ContextWindow)
	}

	undeclared := estimateFor(t, 0, true, 200_000)
	if undeclared.ContextWindow != 200_000 {
		t.Errorf("ContextWindow = %d, want the agent window when the row declares none", undeclared.ContextWindow)
	}
}
