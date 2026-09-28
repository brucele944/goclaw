package pipeline

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// overBudgetDeps forces the prune stage down its compaction path and then keeps
// the history over budget: PruneMessages reduces nothing, and CompactMessages
// returns the history unchanged — exactly what the production callback does when
// compactMessagesInPlace fails (it returns the original messages so the loop keeps
// its history, see internal/agent/loop_pipeline_callbacks.go:728-731).
func overBudgetDeps() *PipelineDeps {
	return &PipelineDeps{
		Config: PipelineConfig{
			ContextWindow: 1000, // budget = 1000 - 0 overhead - 100 max tokens = 900
			MaxTokens:     100,
		},
		TokenCounter: &mockTokenCounter{countPerMessage: 100},
		PruneMessages: func(msgs []providers.Message, _ int) ([]providers.Message, PruneStats) {
			return msgs, PruneStats{}
		},
		CompactMessages: func(_ context.Context, msgs []providers.Message, _ string) ([]providers.Message, error) {
			return msgs, nil // compaction failed → history unchanged, still over budget
		},
	}
}

func overBudgetState(msgs int) *RunState {
	state := defaultState()
	history := make([]providers.Message, msgs)
	for i := range history {
		history[i] = providers.Message{Role: "user", Content: "msg"}
	}
	state.Messages.SetHistory(history)
	return state
}

// A run that aborts because it cannot get back under the context budget must still
// explain itself: it never reaches the think stage, so without this the channel
// receives nothing at all (Telegram outage 2026-09-28).
func TestPruneStage_OverBudgetAfterFailedCompaction_ExplainsToUser(t *testing.T) {
	t.Parallel()

	stage := NewPruneStage(overBudgetDeps(), nil)
	state := overBudgetState(50)

	if err := stage.Execute(store.WithLocale(context.Background(), "vi"), state); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if stage.Result() != AbortRun {
		t.Fatalf("Result() = %v, want AbortRun", stage.Result())
	}
	if want := i18n.T("vi", i18n.MsgContextOverBudgetAbort); state.Observe.FinalContent != want {
		t.Fatalf("FinalContent = %q, want the localized over-budget notice %q", state.Observe.FinalContent, want)
	}
}

// Content a stage already produced is never replaced by the abort notice.
func TestPruneStage_OverBudgetAfterFailedCompaction_KeepsExistingContent(t *testing.T) {
	t.Parallel()

	stage := NewPruneStage(overBudgetDeps(), nil)
	state := overBudgetState(50)
	state.Observe.FinalContent = "partial answer already streamed"

	if err := stage.Execute(context.Background(), state); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if stage.Result() != AbortRun {
		t.Fatalf("Result() = %v, want AbortRun", stage.Result())
	}
	if state.Observe.FinalContent != "partial answer already streamed" {
		t.Fatalf("FinalContent = %q, want the pre-existing content to be kept", state.Observe.FinalContent)
	}
}
