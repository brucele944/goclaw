package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// ChatCompletionsHandler handles POST /v1/chat/completions (OpenAI-compatible).
type ChatCompletionsHandler struct {
	agents      *agent.Router
	sessions    store.SessionStore
	isManaged   bool
	rateLimiter func(string) bool // rate limit check: key → allowed (nil = no limit)
	postTurn    tools.PostTurnProcessor
	providerReg *providers.Registry // resolves a `<provider>/<model>` body model to a live provider
	events      bus.EventPublisher  // run event broadcast (real SSE deltas); nil = one buffered chunk
}

// SetPostTurnProcessor sets the post-turn processor for team task dispatch.
func (h *ChatCompletionsHandler) SetPostTurnProcessor(pt tools.PostTurnProcessor) {
	h.postTurn = pt
}

// SetProviderRegistry lets the endpoint accept a `<provider>/<model>` model value
// as a per-request override — the model identity the capability DTO and the UIs
// hand around. Without a registry every model value stays a bare model id for the
// agent's own provider.
func (h *ChatCompletionsHandler) SetProviderRegistry(reg *providers.Registry) {
	h.providerReg = reg
}

// SetEventPublisher wires the run event broadcast so a streaming response
// forwards the model's deltas as they are produced instead of one buffered
// chunk. Without it the stream still answers, with a single content chunk.
func (h *ChatCompletionsHandler) SetEventPublisher(pub bus.EventPublisher) {
	h.events = pub
}

// NewChatCompletionsHandler creates a handler for the chat completions endpoint.
func NewChatCompletionsHandler(agents *agent.Router, sess store.SessionStore, isManaged bool) *ChatCompletionsHandler {
	return &ChatCompletionsHandler{
		agents:    agents,
		sessions:  sess,
		isManaged: isManaged,
	}
}

// SetRateLimiter sets the rate limiter function for HTTP requests.
func (h *ChatCompletionsHandler) SetRateLimiter(fn func(string) bool) {
	h.rateLimiter = fn
}

type chatCompletionsRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	User     string        `json:"user,omitempty"`

	// Per-request generation options. Applied to this run's provider calls only;
	// the agent's own configuration is never modified.
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`

	// Tool surface declared by the caller. When present the run becomes a
	// passthrough: the model may call these tools, and the calls come back in the
	// response for the caller to execute — they are never executed here.
	Tools      []chatTool      `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	StreamOptions *chatStreamOptions `json:"stream_options,omitempty"`

	// Parameters this endpoint cannot honour. Declared so a caller that sends them
	// gets a precise error instead of silently different behaviour.
	N    *int     `json:"n,omitempty"`
	Stop []string `json:"stop,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// ReasoningContent carries thinking-model output (DeepSeek/Kimi style) so a
	// caller can show it; it is never part of the answer text.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type chatToolCall struct {
	// Index is set on streamed deltas only (OpenAI numbering); the final message
	// carries whole calls.
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatToolCallFunc `json:"function"`
}

// chatToolCallFunc is the {name, arguments} pair of a tool call: the name lands
// on the first streamed delta, the arguments follow as a JSON string (empty on the
// name delta, name empty once arguments start streaming).
type chatToolCallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func newChatToolCallFunc(name, args string) chatToolCallFunc {
	return chatToolCallFunc{Name: name, Arguments: args}
}

type chatCompletionsResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int          `json:"index"`
	Message      *chatMessage `json:"message,omitempty"`
	Delta        *chatMessage `json:"delta,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// runPlan is everything the handler resolved for one request: which agent runs,
// with which per-request overrides, and what the response should echo back.
type runPlan struct {
	agentID     string
	request     agent.RunRequest
	displayName string // model value echoed in the response (what the caller asked for)
}

func (h *ChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)

	if r.Method != http.MethodPost {
		http.Error(w, i18n.T(locale, i18n.MsgMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// Auth + RBAC check (gateway token or API key, operator required for POST)
	auth := resolveAuth(r)
	if !auth.Authenticated {
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", i18n.T(locale, i18n.MsgInvalidAuth))
		return
	}
	if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
		writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", i18n.T(locale, i18n.MsgPermissionDenied, "/v1/chat/completions"))
		return
	}

	// Inject tenant, role, user, and locale into context for downstream stores/tools.
	r = r.WithContext(enrichContext(r.Context(), r, auth))

	// Rate limit check (per IP or bearer token)
	if h.rateLimiter != nil {
		key := r.RemoteAddr
		if token := extractBearerToken(r); token != "" {
			key = "token:" + token
		}
		if !h.rateLimiter(key) {
			w.Header().Set("Retry-After", "60")
			writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", i18n.T(locale, i18n.MsgRateLimitExceeded))
			return
		}
	}

	// Limit request body size to prevent DoS
	const maxRequestBodySize = 1 << 20 // 1MB
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req chatCompletionsRequest
	if !bindJSON(w, r, locale, &req) {
		return
	}

	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", i18n.T(locale, i18n.MsgMsgsRequired))
		return
	}

	// The endpoint is stateless: the caller sends the whole transcript. It either
	// ends with the user turn to answer, or with a tool result being fed back after
	// a tool call this endpoint returned — the model then answers from that result.
	// Any other trailing role (system, assistant to continue) needs conversation
	// state this endpoint does not keep, so it is rejected rather than dropped.
	switch req.Messages[len(req.Messages)-1].Role {
	case "user":
		if strings.TrimSpace(req.Messages[len(req.Messages)-1].Content) == "" {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", i18n.T(locale, i18n.MsgNoUserMessage))
			return
		}
	case "tool":
		// continuation: no new input turn, the transcript is replayed as-is
	default:
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			i18n.T(locale, i18n.MsgChatCompletionsLastRole, req.Messages[len(req.Messages)-1].Role))
		return
	}

	// Reject what this endpoint cannot honour instead of dropping it: a caller that
	// sends `n` > 1 or `stop` sequences expects that behaviour.
	if req.N != nil && *req.N > 1 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			i18n.T(locale, i18n.MsgChatCompletionsInvalidParam, "n", "only n=1 is supported"))
		return
	}
	if len(req.Stop) > 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			i18n.T(locale, i18n.MsgChatCompletionsInvalidParam, "stop", "stop sequences are not supported"))
		return
	}

	clientTools, err := convertClientTools(req.Tools)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			i18n.T(locale, i18n.MsgChatCompletionsInvalidParam, "tools", err.Error()))
		return
	}
	toolChoice, err := parseToolChoice(req.ToolChoice)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error",
			i18n.T(locale, i18n.MsgChatCompletionsInvalidParam, "tool_choice", err.Error()))
		return
	}

	plan, httpStatus, err := h.planRun(r, &req, locale, clientTools, toolChoice)
	if err != nil {
		writeOpenAIError(w, httpStatus, "invalid_request_error", err.Error())
		return
	}

	loop, err := h.agents.Get(r.Context(), plan.agentID)
	if err != nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
			i18n.T(locale, i18n.MsgNotFound, "agent", plan.agentID))
		return
	}

	slog.Info("chat completions request",
		"agent", plan.agentID, "stream", req.Stream, "user", plan.request.UserID,
		"model_override", plan.request.ModelOverride, "provider_override", plan.request.ProviderOverride != nil,
		"client_tools", len(plan.request.ClientTools))

	if req.Stream {
		h.handleStream(w, r, loop, plan, req.StreamOptions)
		return
	}
	h.handleNonStream(w, r, loop, plan)
}

// planRun resolves the agent, the per-request model/provider overrides, the
// caller's transcript and the run request for one call.
func (h *ChatCompletionsHandler) planRun(r *http.Request, req *chatCompletionsRequest, locale string, clientTools []providers.ToolDefinition, toolChoice any) (*runPlan, int, error) {
	ctx := r.Context()
	agentID := extractAgentID(r, req.Model)
	userID := store.UserIDFromContext(ctx) // resolved by enrichContext (respects API key owner binding)
	if h.isManaged && userID == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("%s", i18n.T(locale, i18n.MsgUserIDHeader))
	}

	// Model selection. `goclaw:<agent>`/`agent:<agent>` select the agent, and so
	// does any other `model` value that is not a resolvable `<provider>/<model>`
	// reference — that keeps the pre-existing behaviour for callers that pass a
	// placeholder, while the composite identity the capability DTO hands out is
	// honoured as a per-request override (prefix pins the provider). The split is
	// only a reference when the prefix names a provider this tenant can reach, so a
	// vendor model id that itself contains a slash (openrouter's "openai/gpt-5.5")
	// is not mistaken for one. X-GoClaw-Model always overrides, bare or composite:
	// it is documented as a model override, not an agent selector.
	var (
		modelOverride    string
		providerOverride providers.Provider
	)
	displayName := strings.TrimSpace(req.Model)
	if headerRef := strings.TrimSpace(r.Header.Get("X-GoClaw-Model")); headerRef != "" {
		displayName = headerRef
		if prov, modelID, ok := h.resolveProviderRef(ctx, headerRef); ok {
			providerOverride, modelOverride = prov, modelID
		} else {
			modelOverride = headerRef
		}
	} else if bodyRef := strings.TrimSpace(req.Model); bodyRef != "" && !isAgentSelectorModel(bodyRef) {
		if prov, modelID, ok := h.resolveProviderRef(ctx, bodyRef); ok {
			providerOverride, modelOverride = prov, modelID
		}
	}

	runID := uuid.NewString()
	// Include userID in session key for multi-tenant isolation
	sessionSuffix := "http-" + runID[:8]
	if userID != "" {
		sessionSuffix = "http-" + userID + "-" + runID[:8]
	}
	sessionKey := sessions.SessionKey(agentID, sessionSuffix)

	// Replay the caller's transcript so the agent sees the whole conversation. A
	// trailing user turn is the run's input message (persisted by the loop itself);
	// a trailing tool result is a continuation, so the whole transcript is replayed
	// and the run carries no new input turn. The session is unique to this request,
	// so the replayed transcript never leaks into another conversation.
	prior, input := splitTranscript(req.Messages)
	h.seedClientHistory(ctx, sessionKey, prior)

	return &runPlan{
		agentID:     agentID,
		displayName: displayName,
		request: agent.RunRequest{
			SessionKey:       sessionKey,
			Message:          input,
			Channel:          "http",
			ChatID:           "api",
			RunID:            runID,
			UserID:           userID,
			Stream:           req.Stream,
			ModelOverride:    modelOverride,
			ProviderOverride: providerOverride,
			Temperature:      req.Temperature,
			MaxTokens:        req.MaxTokens,
			ToolChoice:       toolChoice,
			ClientTools:      clientTools,
		},
	}, http.StatusOK, nil
}

// splitTranscript separates the caller's transcript into the messages to replay
// as history and the input turn to run. A trailing user message is the input; any
// other trailing role (a tool result fed back after a tool call) means the
// conversation already ends in the transcript, so every message is history and
// the run has no new input turn.
func splitTranscript(msgs []chatMessage) (prior []chatMessage, input string) {
	if len(msgs) == 0 {
		return nil, ""
	}
	if last := msgs[len(msgs)-1]; last.Role == "user" {
		return msgs[:len(msgs)-1], last.Content
	}
	return msgs, ""
}

// seedClientHistory replays the caller's prior turns into the run's session so
// the agent sees the full conversation. Roles the run cannot place (unknown
// values, empty turns) are skipped; a tool turn keeps its call id so an
// assistant tool call and its result stay paired.
func (h *ChatCompletionsHandler) seedClientHistory(ctx context.Context, sessionKey string, msgs []chatMessage) {
	if h.sessions == nil || len(msgs) == 0 {
		return
	}
	for _, m := range msgs {
		switch m.Role {
		case "system", "user", "assistant", "tool":
		default:
			slog.Debug("chat completions: skipping unsupported history role", "role", m.Role)
			continue
		}
		msg := providers.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name == "" {
				continue
			}
			args := map[string]any{}
			if strings.TrimSpace(tc.Function.Arguments) != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					slog.Debug("chat completions: history tool call arguments dropped", "tool", tc.Function.Name, "error", err)
					args = map[string]any{}
				}
			}
			msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		h.sessions.AddMessage(ctx, sessionKey, msg)
	}
}

// handleNonStream runs the agent and answers with a single OpenAI-shaped message.
func (h *ChatCompletionsHandler) handleNonStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, plan *runPlan) {
	locale := store.LocaleFromContext(r.Context())
	ctx, drainTeamDispatch := tools.InjectTeamDispatch(r.Context(), h.postTurn)
	defer drainTeamDispatch()

	result, err := loop.Run(ctx, plan.request)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", i18n.T(locale, i18n.MsgInternalError, err.Error()))
		return
	}

	msg := &chatMessage{Role: "assistant", ReasoningContent: result.Thinking}
	finishReason := "stop"
	// Text the model produced alongside its calls is kept: OpenAI returns both.
	msg.Content = SignFileURLs(result.Content, FileSigningKey())
	if len(result.ToolCalls) > 0 {
		// Client-owned tool calls: the caller executes them.
		msg.ToolCalls = chatToolCallsFromProviders(result.ToolCalls)
		finishReason = "tool_calls"
	} else {
		finishReason = normalizeFinishReason(result.FinishReason)
	}

	resp := chatCompletionsResponse{
		ID:      "chatcmpl-" + plan.request.RunID[:8],
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   plan.displayName,
		Choices: []chatChoice{{Index: 0, Message: msg, FinishReason: finishReason}},
	}
	if result.Usage != nil {
		resp.Usage = &chatUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleStream runs the agent while forwarding its streamed deltas as SSE
// chunks, then closes with the finish reason (and usage when requested).
func (h *ChatCompletionsHandler) handleStream(w http.ResponseWriter, r *http.Request, loop agent.Agent, plan *runPlan, streamOpts *chatStreamOptions) {
	locale := store.LocaleFromContext(r.Context())
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", i18n.T(locale, i18n.MsgStreamingNotSupported))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + plan.request.RunID[:8]
	model := plan.displayName

	// Opening chunk: role only, as OpenAI streams it.
	writeSSEChunk(w, flusher, completionID, model, &chatMessage{Role: "assistant"}, "")

	// This run's events are queued for the client. Broadcast runs the callback
	// synchronously on the run's goroutine while holding the bus lock, so the
	// callback only appends to a queue and signals — it must never block or write
	// to the response. The queue is a slice rather than a buffered channel so a
	// slow client can delay its own deltas but can never lose one.
	var (
		mu      sync.Mutex
		pending []agent.AgentEvent
		stopped bool
	)
	notify := make(chan struct{}, 1)
	if h.events != nil {
		subID := "http-sse-" + plan.request.RunID
		h.events.Subscribe(subID, func(ev bus.Event) {
			if ev.Name != protocol.EventAgent {
				return
			}
			ae, ok := ev.Payload.(agent.AgentEvent)
			if !ok || ae.RunID != plan.request.RunID {
				return
			}
			mu.Lock()
			if stopped {
				mu.Unlock()
				return
			}
			pending = append(pending, ae)
			mu.Unlock()
			select {
			case notify <- struct{}{}:
			default:
			}
		})
		defer func() {
			mu.Lock()
			stopped = true
			pending = nil
			mu.Unlock()
			h.events.Unsubscribe(subID)
		}()
	}

	var streamed strings.Builder
	forward := func(ae agent.AgentEvent) {
		content := payloadString(ae.Payload, "content")
		if content == "" {
			return
		}
		switch ae.Type {
		case protocol.ChatEventThinking:
			writeSSEChunk(w, flusher, completionID, model,
				&chatMessage{Role: "assistant", ReasoningContent: content}, "")
		case protocol.ChatEventChunk:
			streamed.WriteString(content)
			writeSSEChunk(w, flusher, completionID, model,
				&chatMessage{Role: "assistant", Content: content}, "")
		}
	}

	type runOutcome struct {
		result *agent.RunResult
		err    error
	}
	outcome := make(chan runOutcome, 1)
	go func() {
		ctx, drainTeamDispatch := tools.InjectTeamDispatch(r.Context(), h.postTurn)
		defer drainTeamDispatch()
		res, err := loop.Run(ctx, plan.request)
		outcome <- runOutcome{result: res, err: err}
	}()

	drain := func() {
		for {
			mu.Lock()
			batch := pending
			pending = nil
			mu.Unlock()
			if len(batch) == 0 {
				return
			}
			for _, ae := range batch {
				forward(ae)
			}
		}
	}

	var final runOutcome
	for done := false; !done; {
		select {
		case <-notify:
			drain()
		case out := <-outcome:
			final = out
			done = true
		}
	}
	// Run events are broadcast synchronously on the run goroutine, so every delta
	// is already queued by the time Run returns; drain before closing out.
	drain()

	var usage *chatUsage
	if final.err != nil {
		writeSSEChunk(w, flusher, completionID, model,
			&chatMessage{Content: i18n.T(locale, i18n.MsgInternalError, final.err.Error())}, "stop")
	} else if final.result != nil {
		if len(final.result.ToolCalls) > 0 {
			// Client-owned tool calls: stream them in OpenAI's indexed delta shape
			// (name first, then the arguments) and end the turn.
			for i, tc := range final.result.ToolCalls {
				index := i
				writeSSEChunk(w, flusher, completionID, model,
					&chatMessage{Role: "assistant", ToolCalls: []chatToolCall{{
						Index: &index, ID: tc.ID, Type: "function",
						Function: newChatToolCallFunc(tc.Name, ""),
					}}}, "")
				args, err := json.Marshal(tc.Arguments)
				if err != nil {
					continue
				}
				writeSSEChunk(w, flusher, completionID, model,
					&chatMessage{Role: "assistant", ToolCalls: []chatToolCall{{
						Index:    &index,
						Function: newChatToolCallFunc("", string(args)),
					}}}, "")
			}
			writeSSEChunk(w, flusher, completionID, model, &chatMessage{Role: "assistant"}, "tool_calls")
		} else {
			// Deltas carried the model's text as produced. Anything the caller is
			// still missing — no event publisher wired, or a file URL that only
			// becomes signable once the whole text is known — is sent as the tail
			// when the delivered text is a prefix of the final answer.
			signed := SignFileURLs(final.result.Content, FileSigningKey())
			if remainder, ok := strings.CutPrefix(signed, streamed.String()); ok && remainder != "" {
				writeSSEChunk(w, flusher, completionID, model, &chatMessage{Content: remainder}, "")
			}
			writeSSEChunk(w, flusher, completionID, model, &chatMessage{Role: "assistant"},
				normalizeFinishReason(final.result.FinishReason))
		}
		if final.result.Usage != nil {
			usage = &chatUsage{
				PromptTokens:     final.result.Usage.PromptTokens,
				CompletionTokens: final.result.Usage.CompletionTokens,
				TotalTokens:      final.result.Usage.TotalTokens,
			}
		}
	}

	// OpenAI sends the usage in a final chunk with no choices when the caller asks
	// for it via stream_options.include_usage.
	if streamOpts != nil && streamOpts.IncludeUsage && usage != nil {
		writeSSEUsage(w, flusher, completionID, model, usage)
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, id, model string, delta *chatMessage, finishReason string) {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": nilIfEmpty(finishReason),
		}},
	}

	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeSSEUsage(w http.ResponseWriter, flusher http.Flusher, id, model string, usage *chatUsage) {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{},
		"usage":   usage,
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// writeOpenAIError writes an OpenAI-compatible error envelope. Values are
// JSON-encoded rather than formatted into a template: messages embed quoted
// identifiers, which a raw fmt.Sprintf would turn into invalid JSON.
func writeOpenAIError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    errType,
		},
	})
}

// isAgentSelectorModel reports whether a model value selects the agent rather
// than naming a model ("goclaw:<agent>" / "agent:<agent>").
func isAgentSelectorModel(model string) bool {
	m := strings.TrimSpace(model)
	return strings.HasPrefix(m, "goclaw:") || strings.HasPrefix(m, "agent:")
}

// resolveProviderRef resolves a model reference against the tenant's providers.
// ok is true only when the reference is `<provider>/<model>` and the prefix names
// a provider this tenant can reach, in which case model is the model half; every
// other value is not a provider reference, and the caller keeps its own provider.
func (h *ChatCompletionsHandler) resolveProviderRef(ctx context.Context, ref string) (prov providers.Provider, model string, ok bool) {
	if h.providerReg == nil {
		return nil, "", false
	}
	providerName, modelID, isRef := providers.SplitModelRef(ref, func(string) bool { return true })
	if !isRef {
		return nil, "", false
	}
	prov, err := h.providerReg.GetForTenant(store.TenantIDFromContext(ctx), providerName)
	if err != nil {
		return nil, "", false
	}
	return prov, modelID, true
}

// convertClientTools maps the caller's OpenAI tool definitions onto the run's
// tool surface. Function tools only: a provider-native tool type (e.g.
// "image_generation") is served inside the provider and cannot be called back.
func convertClientTools(tools []chatTool) ([]providers.ToolDefinition, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	defs := make([]providers.ToolDefinition, 0, len(tools))
	for _, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return nil, fmt.Errorf("tool type %q is not callable by this endpoint, only \"function\" tools are", t.Type)
		}
		name := strings.TrimSpace(t.Function.Name)
		if name == "" {
			return nil, fmt.Errorf("a tool definition has no function.name")
		}
		schema := &providers.ToolFunctionSchema{
			Name:        name,
			Description: t.Function.Description,
		}
		if len(t.Function.Parameters) > 0 {
			var params map[string]any
			if err := json.Unmarshal(t.Function.Parameters, &params); err != nil {
				return nil, fmt.Errorf("tool %q has invalid parameters JSON: %v", name, err)
			}
			schema.Parameters = params
		}
		defs = append(defs, providers.ToolDefinition{Type: "function", Function: schema})
	}
	return defs, nil
}

// parseToolChoice validates the OpenAI tool_choice forms and returns the value to
// forward to the provider: the strings "auto"/"none"/"required", or the object form
// naming a function. The object is passed through verbatim — the provider is the
// authority on whether the named function exists in the tool list it was given.
func parseToolChoice(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var choice string
	if err := json.Unmarshal(raw, &choice); err == nil {
		switch choice {
		case "auto", "none", "required":
			return choice, nil
		default:
			return nil, fmt.Errorf("unsupported value %q; expected \"auto\", \"none\" or \"required\", or an object", choice)
		}
	}
	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err != nil {
		return nil, fmt.Errorf("unsupported value %s; expected \"auto\", \"none\" or \"required\", or {\"type\":\"function\",\"function\":{\"name\":\"…\"}}", string(raw))
	}
	if named.Type != "function" || strings.TrimSpace(named.Function.Name) == "" {
		return nil, fmt.Errorf("unsupported value %s; expected \"auto\", \"none\" or \"required\", or {\"type\":\"function\",\"function\":{\"name\":\"…\"}}", string(raw))
	}
	var forwarded any
	if err := json.Unmarshal(raw, &forwarded); err != nil {
		return nil, fmt.Errorf("unsupported value %s", string(raw))
	}
	return forwarded, nil
}

// normalizeFinishReason maps a provider finish reason onto the OpenAI values.
// An unset reason (run ended by a guard, empty response) reads as "stop".
func normalizeFinishReason(reason string) string {
	switch reason {
	case "length", "tool_calls", "stop", "content_filter":
		return reason
	case "":
		return "stop"
	default:
		return "stop"
	}
}

func chatToolCallsFromProviders(calls []providers.ToolCall) []chatToolCall {
	out := make([]chatToolCall, 0, len(calls))
	for _, tc := range calls {
		args, err := json.Marshal(tc.Arguments)
		if err != nil {
			args = []byte("{}")
		}
		out = append(out, chatToolCall{
			ID:       tc.ID,
			Type:     "function",
			Function: newChatToolCallFunc(tc.Name, string(args)),
		})
	}
	return out
}

type chatToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// payloadString reads a string field from an agent event payload. The loop emits
// chunk/thinking payloads as map[string]string; other producers use map[string]any.
func payloadString(payload any, key string) string {
	switch p := payload.(type) {
	case map[string]string:
		return p[key]
	case map[string]any:
		s, _ := p[key].(string)
		return s
	}
	return ""
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
