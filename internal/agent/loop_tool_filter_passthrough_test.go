package agent

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/pipeline"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// TestBuildFilteredToolsClientToolsReplaceAgentSurface — a run whose tools are
// declared by the caller (OpenAI-compatible passthrough) offers exactly those
// definitions: the agent's registry tools are not merged in, because the caller
// executes the calls it receives.
func TestBuildFilteredToolsClientToolsReplaceAgentSurface(t *testing.T) {
	t.Parallel()
	loop := &Loop{id: "test-agent", maxIterations: 4}
	clientTools := []providers.ToolDefinition{{
		Type:     "function",
		Function: &providers.ToolFunctionSchema{Name: "get_weather"},
	}}
	req := &RunRequest{RunID: "run-1", ClientTools: clientTools}
	build := loop.makeBuildFilteredTools(req)

	state := &pipeline.RunState{Input: &pipeline.RunInput{RunID: "run-1", ClientTools: clientTools}, Iteration: 0}
	defs, err := build(state)
	if err != nil {
		t.Fatalf("buildFilteredTools error: %v", err)
	}
	if len(defs) != 1 || defs[0].Function == nil || defs[0].Function.Name != "get_weather" {
		t.Fatalf("defs = %+v, want exactly the caller's get_weather definition", defs)
	}

	// Also on the final iteration, which strips the agent's own tools: the caller's
	// surface is constant for the run.
	final := &pipeline.RunState{Input: state.Input, Iteration: loop.maxIterations}
	defs, err = build(final)
	if err != nil {
		t.Fatalf("buildFilteredTools error on final iteration: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("final-iteration defs = %+v, want the caller's definition", defs)
	}
}
