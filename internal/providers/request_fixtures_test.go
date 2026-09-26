package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestProviderRequestFixtures pins the OUTBOUND request bodies (and, where the
// wire family has no HTTP body, the argv / JSON-RPC envelope) of every live
// provider wire family against stored fixtures under testdata/fixtures/.
//
// Each subtest is named after the production symbol it exercises and drives the
// REAL provider constructor through its real Chat/ChatStream path, observing the
// bytes at the network/subprocess boundary. Comparisons are normalised (parsed,
// volatile keys stripped, keys sorted) — never raw strings — so a reordered
// object passes while a removed or changed field fails loudly.
//
// Layout: testdata/fixtures/<wire>/<case>.json
func TestProviderRequestFixtures(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"NewAnthropicProvider_messages_tools_cache_thinking", fixtureAnthropic},
		{"NewOpenAIProvider_native_prompt_cache_reasoning", fixtureOpenAINative},
		{"NewOpenAIProvider_compat_gateway_no_native_fields", fixtureOpenAICompatGateway},
		{"NewDashScopeProvider_tools_nonstream_chunk_synthesis", fixtureDashScope},
		{"NewOllamaProvider_native_options_num_ctx", fixtureOllama},
		{"NewCodexProvider_responses_input_and_usage_mapping", fixtureCodex},
		{"ClaudeCLIProvider_buildArgs_session_id_then_resume", fixtureClaudeCLI},
		{"ACPProvider_session_prompt_envelope", fixtureACP},
		{"NewVertexProvider_endpoint_and_service_account_auth", fixtureVertex},
		{"NormalizeSchema_gemini_tool_schema_via_openai_route", fixtureGeminiSchemaNormalize},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

// ---------------------------------------------------------------------------
// 1. Anthropic Messages API
// ---------------------------------------------------------------------------

func fixtureAnthropic(t *testing.T) {
	var captured recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = recordedRequest{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"fixture-ok"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":11,"output_tokens":3,"cache_creation_input_tokens":7,"cache_read_input_tokens":5}}`)
	}))
	defer server.Close()

	p := NewAnthropicProvider("fixture-key",
		WithAnthropicBaseURL(server.URL),
		WithAnthropicModel("claude-sonnet-4-5"))

	resp, err := p.Chat(context.Background(), ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			// CacheBoundaryMarker splits the system prompt into a cached stable
			// block plus an uncached dynamic block.
			{Role: "system", Content: "stable fixture prefix" + CacheBoundaryMarker + "dynamic fixture suffix"},
			{Role: "user", Content: "hello fixture"},
		},
		Tools: fixtureTools(),
		Options: map[string]any{
			OptThinkingLevel: "high",
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "fixture-ok" {
		t.Fatalf("response content = %q, want fixture-ok", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 3 ||
		resp.Usage.CacheCreationTokens != 7 || resp.Usage.CacheReadTokens != 5 {
		t.Fatalf("usage mapping = %+v, want prompt=11 completion=3 cache_creation=7 cache_read=5", resp.Usage)
	}

	// Endpoint + headers are part of the wire contract.
	if want := "/messages"; captured.URL != want {
		t.Errorf("request path = %q, want %q", captured.URL, want)
	}
	for header, want := range map[string]string{
		"x-api-key":         "fixture-key",
		"anthropic-version": anthropicAPIVersion,
		// Thinking present → interleaved-thinking beta header.
		"anthropic-beta": "interleaved-thinking-2025-05-14",
		"Content-Type":   "application/json",
	} {
		if got := captured.Header.Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}

	assertJSONFixture(t, "anthropic/chat_tools_thinking.json", jsonBody(t, captured))
}

// ---------------------------------------------------------------------------
// 2 + 3. OpenAI Chat Completions: native vs compatible gateway
// ---------------------------------------------------------------------------

// fixtureOpenAINativeRequest is the one logical request both OpenAI cases send,
// so their fixtures differ only by what the native-only gates add.
func fixtureOpenAINativeRequest() ChatRequest {
	return ChatRequest{
		Model:    "gpt-5.4",
		Messages: fixtureMessages(),
		Tools:    fixtureTools(),
		Options: map[string]any{
			OptMaxTokens:            2048,
			OptThinkingLevel:        "high",
			OptPromptCacheKey:       "fixture-cache-key",
			OptPromptCacheRetention: "24h",
		},
	}
}

func fixtureOpenAINative(t *testing.T) {
	rec := &recordingTransport{
		defaultJSON: `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture-ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":17,"completion_tokens":4,"total_tokens":21}}`,
	}
	// Real native base URL: the developer-role and prompt-cache gates key off it.
	p := NewOpenAIProvider("openai", "fixture-key", "https://api.openai.com/v1", "gpt-5.4").
		WithHTTPClient(&http.Client{Transport: rec})

	resp, err := p.Chat(context.Background(), fixtureOpenAINativeRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "fixture-ok" {
		t.Fatalf("response content = %q, want fixture-ok", resp.Content)
	}

	captured := rec.singleRequest(t)
	if want := "https://api.openai.com/v1/chat/completions"; captured.URL != want {
		t.Errorf("request URL = %q, want %q", captured.URL, want)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer fixture-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer fixture-key")
	}

	body := decodeBody(t, captured)
	// Native-only fields the fixture pins (fail loudly if any is dropped).
	if got := body["prompt_cache_key"]; got != "fixture-cache-key" {
		t.Errorf("prompt_cache_key = %v, want fixture-cache-key", got)
	}
	if got := body["prompt_cache_retention"]; got != "24h" {
		t.Errorf("prompt_cache_retention = %v, want 24h", got)
	}
	if got := body["max_completion_tokens"]; got != float64(2048) {
		t.Errorf("max_completion_tokens = %v, want 2048", got)
	}
	if _, hasLegacy := body["max_tokens"]; hasLegacy {
		t.Errorf("native request must use max_completion_tokens, not max_tokens: %v", body["max_tokens"])
	}
	if got := body["reasoning_effort"]; got != "high" {
		t.Errorf("reasoning_effort = %v, want high", got)
	}
	if got := firstMessageRole(t, body); got != "developer" {
		t.Errorf("first message role = %q, want developer (native OpenAI role mapping)", got)
	}

	assertJSONFixture(t, "openai-native/chat_reasoning_cache.json", jsonBody(t, captured))
}

func fixtureOpenAICompatGateway(t *testing.T) {
	rec := &recordingTransport{
		defaultJSON: `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture-ok"},"finish_reason":"stop"}]}`,
	}
	// OpenRouter-style gateway: not native OpenAI, and the model carries a provider prefix.
	p := NewOpenAIProvider("openrouter", "fixture-key", "https://openrouter.ai/api/v1", "anthropic/claude-sonnet-4-5").
		WithHTTPClient(&http.Client{Transport: rec})

	req := fixtureOpenAINativeRequest()
	req.Model = "anthropic/claude-sonnet-4-5"

	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	captured := rec.singleRequest(t)
	if want := "https://openrouter.ai/api/v1/chat/completions"; captured.URL != want {
		t.Errorf("request URL = %q, want %q", captured.URL, want)
	}

	body := decodeBody(t, captured)
	// Every one of these is native-OpenAI-only; a gateway must never receive them.
	for _, banned := range []string{
		"prompt_cache_key", "prompt_cache_retention",
		"max_completion_tokens", "reasoning_effort", "service_tier", "store",
	} {
		if v, present := body[banned]; present {
			t.Errorf("compat gateway received native-only field %s = %v", banned, v)
		}
	}
	if got := body["max_tokens"]; got != float64(2048) {
		t.Errorf("max_tokens = %v, want 2048 (compat hosts keep the legacy field)", got)
	}
	if got := firstMessageRole(t, body); got != "system" {
		t.Errorf("first message role = %q, want system (developer role is native-only)", got)
	}

	assertJSONFixture(t, "openai-compat/chat_gateway_no_native_fields.json", jsonBody(t, captured))
}

// ---------------------------------------------------------------------------
// 4. DashScope (tools must take the non-streaming path)
// ---------------------------------------------------------------------------

func fixtureDashScope(t *testing.T) {
	// Pin the prompt-cache escape hatch off so the fixture is env-independent.
	t.Setenv("GOCLAW_DISABLE_DASHSCOPE_CACHE", "false")

	rec := &recordingTransport{
		defaultJSON: `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture-ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}`,
	}
	p := NewDashScopeProvider("dashscope", "fixture-key",
		"https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen3-max")
	// Inject the observing client through the embedded OpenAI provider.
	p.OpenAIProvider.WithHTTPClient(&http.Client{Transport: rec})

	var chunks []StreamChunk
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Model:    "qwen3-max",
		Messages: fixtureMessages(),
		Tools:    fixtureTools(),
	}, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "fixture-ok" || resp.FinishReason != "stop" {
		t.Fatalf("response = %+v, want content=fixture-ok finish_reason=stop", resp)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 11 {
		t.Fatalf("usage = %+v, want total_tokens=11", resp.Usage)
	}

	// Tools present → DashScope must have taken the non-streaming path and
	// synthesised the chunk sequence for the caller.
	captured := rec.singleRequest(t)
	body := decodeBody(t, captured)
	if stream, present := body["stream"]; !present || stream != false {
		t.Errorf("DashScope tools request stream = %v (present=%v), want false — stream:true is unsupported with tools", stream, present)
	}
	gotChunks := make([]StreamChunk, 0, len(chunks))
	for _, c := range chunks {
		gotChunks = append(gotChunks, c)
	}
	assertJSONFixture(t, "dashscope/chat_tools_nonstream.json", jsonBody(t, captured))
	assertJSONFixture(t, "dashscope/tools_chunk_synthesis.json", rawJSON(t, gotChunks))
}

// ---------------------------------------------------------------------------
// 5. Ollama native /api/chat
// ---------------------------------------------------------------------------

func fixtureOllama(t *testing.T) {
	var captured recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = recordedRequest{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"qwen3:8b","message":{"role":"assistant","content":"fixture-ok"},`+
			`"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":2}`)
	}))
	defer server.Close()

	numCtx := 32768
	p := NewOllamaProvider("ollama", server.URL, "qwen3:8b", &numCtx, server.Client())

	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:    "qwen3:8b",
		Messages: fixtureMessages(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "fixture-ok" || resp.FinishReason != "stop" {
		t.Fatalf("response = %+v, want content=fixture-ok finish_reason=stop", resp)
	}

	if want := "/api/chat"; captured.URL != want {
		t.Errorf("request path = %q, want %q (native endpoint, not /v1/chat/completions)", captured.URL, want)
	}
	body := decodeBody(t, captured)
	opts, _ := body["options"].(map[string]any)
	if got := opts["num_ctx"]; got != float64(32768) {
		t.Errorf("options.num_ctx = %v, want 32768 (must be honored on the native endpoint)", got)
	}

	assertJSONFixture(t, "ollama/native_chat_num_ctx.json", jsonBody(t, captured))
}

// ---------------------------------------------------------------------------
// 6. Codex Responses API (+ usageFromCodexUsage mapping)
// ---------------------------------------------------------------------------

func fixtureCodex(t *testing.T) {
	var captured recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = recordedRequest{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: body}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not implement http.Flusher")
			return
		}
		events := []map[string]any{
			{"type": "response.output_item.added", "item_id": "msg_fixture", "output_index": 0,
				"item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant"}},
			{"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": "fixture-ok"},
			{"type": "response.completed", "response": map[string]any{
				"id":     "resp_fixture",
				"object": "response",
				"model":  "gpt-5.3-codex",
				"status": "completed",
				"output": []any{
					map[string]any{"type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": "fixture-ok"}}},
				},
				"usage": map[string]any{
					"input_tokens":          100,
					"output_tokens":         20,
					"total_tokens":          120,
					"input_tokens_details":  map[string]any{"cached_tokens": 64},
					"output_tokens_details": map[string]any{"reasoning_tokens": 7},
				},
			}},
		}
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", rawJSON(t, ev))
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := NewCodexProvider("codex", &staticTokenSource{token: "fixture-token"}, server.URL, "gpt-5.3-codex")

	var chunks []StreamChunk
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Model: "gpt-5.3-codex",
		Messages: []Message{
			{Role: "system", Content: "You are a fixture assistant."},
			{Role: "user", Content: "hello fixture"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_abc", Name: "lookup", Arguments: map[string]any{"id": "42"}}}},
			{Role: "tool", ToolCallID: "call_abc", ToolName: "lookup", Content: `{"found":true}`},
		},
		Tools:   fixtureTools(),
		Options: map[string]any{OptThinkingLevel: "high"},
	}, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "fixture-ok" {
		t.Fatalf("response content = %q, want fixture-ok", resp.Content)
	}
	// Stream mapping: the text delta is surfaced and the stream is terminated.
	assertJSONFixture(t, "codex/stream_chunks.json", rawJSON(t, chunks))

	// usageFromCodexUsage mapping asserted from the canned usage block above.
	wantUsage := &Usage{
		PromptTokens:                      100,
		CompletionTokens:                  20,
		TotalTokens:                       120,
		CacheReadTokens:                   64,
		PromptTokensIncludeCachedSegments: true,
		ThinkingTokens:                    7,
	}
	if resp.Usage == nil || *resp.Usage != *wantUsage {
		t.Errorf("usage mapping = %+v, want %+v", resp.Usage, wantUsage)
	}

	if want := "/codex/responses"; captured.URL != want {
		t.Errorf("request path = %q, want %q", captured.URL, want)
	}
	for header, want := range map[string]string{
		"Authorization": "Bearer fixture-token",
		"OpenAI-Beta":   "responses=v1",
	} {
		if got := captured.Header.Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}

	body := decodeBody(t, captured)
	if stream, _ := body["stream"].(bool); !stream {
		t.Errorf("Codex Responses requests must stream: stream = %v", body["stream"])
	}
	if store, present := body["store"]; !present || store != false {
		t.Errorf("store = %v (present=%v), want false", store, present)
	}
	assertJSONFixture(t, "codex/responses_input_items.json", jsonBody(t, captured))
}

// ---------------------------------------------------------------------------
// 7. Claude CLI argv (session-id vs resume)
// ---------------------------------------------------------------------------

func fixtureClaudeCLI(t *testing.T) {
	stub := buildStubCLI(t)

	// Fake HOME so session-file lookups land in a temp dir we control.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	workRoot := t.TempDir()
	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(workRoot))

	const sessionKey = "fixture-cli-session"
	req := ChatRequest{
		Model:    "sonnet",
		Messages: []Message{{Role: "user", Content: "hello-fixture"}},
		Options: map[string]any{
			OptSessionKey:       sessionKey,
			OptAllowedToolNames: []string{"exec"},
		},
	}

	// First call: no CLI session file on disk → --session-id creates the session.
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("first Chat content = %q, want stub-ok", resp.Content)
	}

	invocations := stubInvocations(t, logPath)
	if len(invocations) != 1 {
		t.Fatalf("invocations after first call = %d, want 1: %v", len(invocations), invocations)
	}
	firstArgs := strings.Split(invocations[0], " ")
	for _, want := range []string{"-p", "--output-format", "json", "--model", "sonnet", "--session-id", "--verbose"} {
		if !containsArg(firstArgs, want) {
			t.Errorf("first invocation missing %q: %v", want, firstArgs)
		}
	}
	if !containsArg(firstArgs, "--") || firstArgs[len(firstArgs)-1] != "hello-fixture" {
		t.Errorf("first invocation must pass the user message positionally after --: %v", firstArgs)
	}
	if containsArg(firstArgs, "--resume") {
		t.Errorf("first invocation must not resume: %q", invocations[0])
	}
	assertJSONFixture(t, "claude-cli/argv_first_call.json", rawJSON(t, firstArgs))

	// Create the CLI session file the provider looks for, then call again:
	// the CLI already owns the session → --resume, and no --session-id.
	sessionID := deriveSessionUUID(sessionKey)
	workDir := p.ensureWorkDir(sessionKey)
	sessionFile := claudeSessionFilePath(workDir, sessionID)
	if sessionFile == "" {
		t.Fatal("claudeSessionFilePath returned empty; cannot drive the resume path")
	}
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write fake CLI session file: %v", err)
	}

	resp, err = p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("resume Chat: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("resume Chat content = %q, want stub-ok", resp.Content)
	}

	invocations = stubInvocations(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations after resume call = %d, want 2: %v", len(invocations), invocations)
	}
	resumeArgs := strings.Split(invocations[1], " ")
	if !containsArg(resumeArgs, "--resume") {
		t.Errorf("resume invocation missing --resume: %v", resumeArgs)
	}
	if containsArg(resumeArgs, "--session-id") {
		t.Errorf("resume invocation must NOT send --session-id: %v", resumeArgs)
	}
	assertJSONFixture(t, "claude-cli/argv_resume.json", rawJSON(t, resumeArgs))
}

// ---------------------------------------------------------------------------
// 8. ACP JSON-RPC envelope
// ---------------------------------------------------------------------------

func fixtureACP(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "acp-inbound.ndjson")
	t.Setenv(acpStubLogEnv, logPath)

	// Point the provider at this test binary running the stub helper: the real
	// subprocess spawn, handshake and session/prompt write all execute.
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("abs test binary: %v", err)
	}
	p := NewACPProvider(bin, []string{"-test.run=^TestACPStubAgentHelper$"}, t.TempDir(), time.Minute, nil)
	defer func() { _ = p.Close() }()

	resp, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "fixture system"},
			{Role: "user", Content: "hello fixture"},
		},
		Options: map[string]any{OptSessionKey: "acp-fixture"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "fixture-ok" || resp.FinishReason != "stop" {
		t.Fatalf("response = %+v, want content=fixture-ok finish_reason=stop", resp)
	}

	frames := acpStubInboundFrames(t, logPath)
	var prompt *map[string]any
	for i := range frames {
		if frames[i]["method"] == "session/prompt" {
			prompt = &frames[i]
		}
	}
	if prompt == nil {
		t.Fatalf("no session/prompt frame reached the ACP agent; frames = %v", frames)
	}
	assertJSONFixture(t, "acp/session_prompt_envelope.json", rawJSON(t, *prompt))

	params, _ := (*prompt)["params"].(map[string]any)
	if params["sessionId"] != acpStubSessionID {
		t.Errorf("session/prompt sessionId = %v, want %s", params["sessionId"], acpStubSessionID)
	}
	promptBlocks, _ := params["prompt"].([]any)
	if len(promptBlocks) != 1 {
		t.Fatalf("session/prompt prompt blocks = %v, want exactly 1 text block", params["prompt"])
	}
	block, _ := promptBlocks[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "fixture system\n\nhello fixture" {
		t.Errorf("session/prompt block = %v, want text block with system prompt folded into the user turn", block)
	}
}

// ---------------------------------------------------------------------------
// 9. Vertex AI endpoint + service-account auth
// ---------------------------------------------------------------------------

func fixtureVertex(t *testing.T) {
	// The endpoint shape comes from the production helper.
	apiBase := VertexDefaultAPIBase("my-proj", "us-central1")
	if apiBase != "https://us-central1-aiplatform.googleapis.com/v1/projects/my-proj/locations/us-central1/endpoints/openapi" {
		t.Fatalf("VertexDefaultAPIBase = %q", apiBase)
	}

	// NewVertexProvider itself only accepts credentials that must be resolved from
	// GCP; assert the wiring it produces with a structurally valid (fake) service
	// account, which does not attempt token minting.
	fakeSA := map[string]any{
		"type":         "service_account",
		"project_id":   "my-proj",
		"private_key":  fakePEM,
		"client_email": "fixture@my-proj.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	saJSON, _ := json.Marshal(fakeSA)
	saProvider, err := NewVertexProvider(context.Background(), VertexConfig{
		CredentialsJSON: string(saJSON),
		ProjectID:       "my-proj",
		Region:          "us-central1",
	})
	if err != nil {
		t.Fatalf("NewVertexProvider: %v", err)
	}
	if saProvider.APIBase() != apiBase {
		t.Errorf("NewVertexProvider APIBase = %q, want %q", saProvider.APIBase(), apiBase)
	}
	if saProvider.ProviderType() != ProviderTypeVertex {
		t.Errorf("NewVertexProvider provider type = %q, want %q", saProvider.ProviderType(), ProviderTypeVertex)
	}

	// Drive the same assembly NewVertexProvider performs — oauth2.Transport over a
	// TokenSource plus WithoutAuthHeader — substituting a stub service-account
	// token source and a recording transport, so the request never leaves the
	// process while the endpoint path and Authorization header stay production-shaped.
	rec := &recordingTransport{
		defaultJSON: `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture-ok"},"finish_reason":"stop"}]}`,
	}
	client := &http.Client{Transport: &oauth2.Transport{
		Source: stubServiceAccountTokenSource("stub-vertex-token"),
		Base:   rec,
	}}
	p := NewOpenAIProvider("vertex", "", apiBase, VertexDefaultModel).
		WithProviderType(ProviderTypeVertex).
		WithHTTPClient(client).
		WithoutAuthHeader()

	if _, err := p.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hello vertex"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	captured := rec.singleRequest(t)
	if got := captured.Header.Get("Authorization"); got != "Bearer stub-vertex-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer stub-vertex-token")
	}

	assertJSONFixture(t, "vertex/chat_endpoint_and_auth.json", rawJSON(t, map[string]any{
		"url":           captured.URL,
		"authorization": captured.Header.Get("Authorization"),
		"body":          json.RawMessage(jsonBody(t, captured)),
	}))
}

// ---------------------------------------------------------------------------
// 10. Gemini-flavoured tool schema normalisation through the live OpenAI route
// ---------------------------------------------------------------------------

func fixtureGeminiSchemaNormalize(t *testing.T) {
	rec := &recordingTransport{}

	// "gemini" provider type selects the Gemini schema profile inside the live
	// request path (buildToolsPayload → CleanToolSchemas → NormalizeSchema).
	p := NewOpenAIProvider("gemini", "fixture-key",
		"https://generativelanguage.googleapis.com/v1beta/openai", "gemini-2.5-flash").
		WithProviderType("gemini_native").
		WithHTTPClient(&http.Client{Transport: rec})

	tools := []ToolDefinition{{
		Type: "function",
		Function: &ToolFunctionSchema{
			Name:        "lookup",
			Description: "Look up a record by id",
			Parameters: map[string]any{
				"$schema":              "https://json-schema.org/draft/2020-12/schema",
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id":     map[string]any{"type": "string", "minLength": 1, "format": "uuid"},
					"filter": map[string]any{"$ref": "#/$defs/filter"},
				},
				"required": []string{"id"},
				"$defs": map[string]any{
					"filter": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"kind": map[string]any{"const": "exact"},
						},
					},
				},
			},
		},
	}}

	if _, err := p.Chat(context.Background(), ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: fixtureMessages(),
		Tools:    tools,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	captured := rec.singleRequest(t)
	assertJSONFixture(t, "openai-schema-normalize/chat_gemini_tool_schema.json", jsonBody(t, captured))

	// The fixture pins the exact cleaned schema; assert the specific removals the
	// Gemini API rejects so the failure message names the offending keyword.
	canonical := canonicalJSON(t, jsonBody(t, captured))
	for _, banned := range []string{`"$ref"`, `"$defs"`, `"definitions"`, `"additionalProperties"`, `"$schema"`, `"minLength"`, `"format"`} {
		if strings.Contains(canonical, banned) {
			t.Errorf("tool schema sent to Gemini still contains %s: %s", banned, canonical)
		}
	}
	// $ref was inlined and const converted to enum (Gemini requires enum).
	if !strings.Contains(canonical, `"kind":{"enum":["exact"]}`) {
		t.Errorf("$defs/$ref were not inlined and const not converted to enum: %s", canonical)
	}
}

// ---------------------------------------------------------------------------
// small assertion helpers
// ---------------------------------------------------------------------------

func decodeBody(t *testing.T, req recordedRequest) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(jsonBody(t, req), &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func firstMessageRole(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("body has no messages array: %v", body["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	role, _ := first["role"].(string)
	return role
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
