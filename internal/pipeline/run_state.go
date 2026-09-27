package pipeline

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/workspace"
)

// RunState is the shared mutable state for a single pipeline run.
// Passed by pointer through all stages.
type RunState struct {
	// Identity (set once at pipeline start, immutable during run)
	Input     *RunInput
	Workspace *workspace.WorkspaceContext
	Model     string
	Provider  providers.Provider

	// Ctx holds enriched context from ContextStage (agent/user/workspace values).
	// Pipeline.Run uses this for all stages after setup completes.
	Ctx context.Context

	// Message buffer (read/write by multiple stages)
	Messages *MessageBuffer

	// Per-stage substates
	Context   ContextState
	Think     ThinkState
	Prune     PruneState
	Tool      ToolState
	Observe   ObserveState
	Compact   CompactState
	Evolution EvolutionState

	// Cross-cutting concerns
	Iteration int
	RunID     string
	ExitCode  StageResult

	// CurrentLLMSpanID is the most recent LLM-call span in this run; tool spans parent to it.
	CurrentLLMSpanID *uuid.UUID
	// CurrentToolSpanID is the most recent tool-call span; post-tool-use hook spans parent to it.
	CurrentToolSpanID *uuid.UUID

	// Calls is the per-call usage breakdown (LLM calls + tool-internal LLM calls),
	// appended during the run. Guarded by callsMu for the parallel tool path.
	Calls   []providers.CallUsage
	callsMu sync.Mutex
}

// NewRunState creates a RunState with identity fields set.
func NewRunState(input *RunInput, ws *workspace.WorkspaceContext, model string, provider providers.Provider) *RunState {
	return &RunState{
		Input:     input,
		Workspace: ws,
		Model:     model,
		Provider:  provider,
		RunID:     input.RunID,
		Messages:  NewMessageBuffer(providers.Message{}),
	}
}

// AppendCall records one call's usage in the run breakdown (thread-safe).
func (rs *RunState) AppendCall(c providers.CallUsage) {
	rs.callsMu.Lock()
	rs.Calls = append(rs.Calls, c)
	rs.callsMu.Unlock()
}

// BuildResult converts final RunState into a RunResult.
func (rs *RunState) BuildResult() *RunResult {
	return &RunResult{
		RunID:          rs.RunID,
		Content:        rs.Observe.FinalContent,
		Thinking:       rs.Observe.FinalThinking,
		TotalUsage:     rs.Think.TotalUsage,
		LastUsage:      rs.Think.LastUsage,
		Iterations:     rs.Iteration,
		ToolCalls:      rs.Tool.TotalToolCalls,
		LoopKilled:     rs.Tool.LoopKilled,
		AsyncToolCalls: rs.Tool.AsyncToolCalls,
		MediaResults:   rs.Tool.MediaResults,
		Deliverables:   rs.Tool.Deliverables,
		BlockReplies:   rs.Observe.BlockReplies,
		LastBlockReply: rs.Observe.LastBlockReply,
		Calls:          rs.Calls,
		FinalToolCalls: rs.Observe.FinalToolCalls,
		FinishReason:   rs.Observe.FinalFinishReason,
	}
}

// RunInput is the pipeline's view of a run request.
// Converted from agent.RunRequest by the adapter in Phase 8.
type RunInput struct {
	SessionKey                 string
	Message                    string
	Media                      []bus.MediaFile
	ForwardMedia               []bus.MediaFile
	Channel                    string
	ChannelType                string
	BitrixPortalDomain         string // bitrix24-only: portal domain for entity URL construction
	ChatTitle                  string
	ChatID                     string
	PeerKind                   string
	RunID                      string
	UserID                     string
	SenderID                   string
	SenderName                 string
	Stream                     bool
	ExtraSystemPrompt          string
	SkillFilter                []string
	HistoryLimit               int
	ToolAllow                  []string
	TelegramManagerPermissions []string
	LightContext               bool
	RunKind                    string
	DelegationID               string
	TeamID                     string
	TeamTaskID                 string
	ParentAgentID              string
	MaxIterations              int
	ModelOverride              string
	HideInput                  bool
	ContentSuffix              string
	LeaderAgentID              string
	WorkspaceChannel           string
	WorkspaceChatID            string
	TeamWorkspace              string

	// ClientTools are caller-declared tool definitions for a passthrough run:
	// they replace the agent's tool surface and the model's calls are returned to
	// the caller instead of being executed. Empty = normal agent run.
	ClientTools []providers.ToolDefinition
}

// MediaResult represents a media file produced during tool execution.
type MediaResult struct {
	Path        string
	ContentType string
	Size        int64
	AsVoice     bool
	// Prompt is the generation prompt for AI-generated media (e.g. create_image).
	// Empty for user-uploaded or non-generated files.
	Prompt string
}

// providerName is the serving provider's name ("" when the run has no provider).
func providerName(p providers.Provider) string {
	if p == nil {
		return ""
	}
	return p.Name()
}

// modelIdentity is the `<provider>/<model>` identity recorded on an assistant
// turn — the same identity the capability DTO serves and the UIs select from, so a
// transcript can show which model answered. Falls back to the bare model when the
// provider is unknown.
func modelIdentity(p providers.Provider, model string) string {
	name := providerName(p)
	if name == "" || model == "" {
		return model
	}
	return name + "/" + model
}
