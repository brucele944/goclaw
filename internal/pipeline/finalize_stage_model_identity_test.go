package pipeline

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// namedProvider reports only its name: the identity of a finished turn needs
// nothing else from the serving provider.
type namedProvider struct{ providers.Provider }

func (namedProvider) Name() string { return "groq" }

// TestFinalizeStage_AssistantTurnCarriesResolvedModel — the persisted assistant
// turn records which model answered, in the same `<provider>/<model>` identity the
// capability DTO and the model pickers use, so chat.history can show it per turn.
func TestFinalizeStage_AssistantTurnCarriesResolvedModel(t *testing.T) {
	t.Parallel()
	var flushed []providers.Message
	deps := &PipelineDeps{
		FlushMessages: func(_ context.Context, _ string, msgs []providers.Message) error {
			flushed = msgs
			return nil
		},
		UpdateMetadata: func(context.Context, string, providers.Usage, providers.Usage, int) error { return nil },
	}
	stage := NewFinalizeStage(deps)
	state := defaultState()
	state.Provider = namedProvider{}
	state.Model = "llama-3.3-70b"
	state.Observe.FinalContent = "answer"

	if err := stage.Execute(context.Background(), state); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(flushed) != 1 {
		t.Fatalf("flushed %d messages, want the single assistant turn: %+v", len(flushed), flushed)
	}
	if flushed[0].Role != "assistant" {
		t.Fatalf("flushed role = %q, want assistant", flushed[0].Role)
	}
	if flushed[0].Model != "groq/llama-3.3-70b" {
		t.Errorf("assistant turn model = %q, want the qualified identity groq/llama-3.3-70b", flushed[0].Model)
	}
	if flushed[0].Provider != "groq" {
		t.Errorf("assistant turn provider = %q, want groq", flushed[0].Provider)
	}
}
