package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

// fixturesDir is the root for the per-wire-family characterization fixtures.
// Layout: testdata/fixtures/<wire>/<case>.json
const fixturesDir = "testdata/fixtures"

// updateFixturesEnv, when truthy, makes assertJSONFixture rewrite the fixture
// file from the observed payload instead of comparing. Used to regenerate
// fixtures after an intentional wire change; never set in CI.
const updateFixturesEnv = "GOCLAW_UPDATE_PROVIDER_FIXTURES"

// ---------------------------------------------------------------------------
// Request capture
// ---------------------------------------------------------------------------

// recordedRequest is one outbound HTTP request observed at the transport boundary.
type recordedRequest struct {
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// recordingTransport captures outbound requests and answers them with a canned
// response. It lets a test drive a provider with its REAL production base URL
// (so endpoint-gated fields such as prompt_cache_key or the developer role are
// exercised exactly as in production) while still observing the exact bytes.
type recordingTransport struct {
	// respond builds the canned response for an observed request. When nil the
	// defaultJSON body is served with status 200.
	respond     func(req recordedRequest) *http.Response
	defaultJSON string

	mu   sync.Mutex
	reqs []recordedRequest
}

func (rt *recordingTransport) RoundTrip(httpReq *http.Request) (*http.Response, error) {
	var body []byte
	if httpReq.Body != nil {
		var err error
		body, err = io.ReadAll(httpReq.Body)
		if err != nil {
			return nil, err
		}
		_ = httpReq.Body.Close()
	}

	rec := recordedRequest{
		Method: httpReq.Method,
		URL:    httpReq.URL.String(),
		Header: httpReq.Header.Clone(),
		Body:   body,
	}

	rt.mu.Lock()
	rt.reqs = append(rt.reqs, rec)
	rt.mu.Unlock()

	if rt.respond != nil {
		resp := rt.respond(rec)
		if resp.Body == nil {
			resp.Body = io.NopCloser(bytes.NewReader(nil))
		}
		resp.Request = httpReq
		return resp, nil
	}

	respBody := rt.defaultJSON
	if respBody == "" {
		respBody = `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"fixture-ok"},"finish_reason":"stop"}]}`
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(respBody)),
		ContentLength: int64(len(respBody)),
		Request:       httpReq,
	}, nil
}

// requests returns a copy of every captured request, in send order.
func (rt *recordingTransport) requests() []recordedRequest {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]recordedRequest(nil), rt.reqs...)
}

// singleRequest asserts exactly one request was sent and returns it.
func (rt *recordingTransport) singleRequest(t *testing.T) recordedRequest {
	t.Helper()
	reqs := rt.requests()
	if len(reqs) != 1 {
		t.Fatalf("captured %d outbound requests, want exactly 1", len(reqs))
	}
	return reqs[0]
}

// ---------------------------------------------------------------------------
// Fixture comparison
// ---------------------------------------------------------------------------

// loadFixture reads a fixture file relative to testdata/fixtures.
func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir, rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

// canonicalJSON parses data and re-marshals it with sorted object keys, dropping
// every object key named in dropKeys (at any depth). Volatile values such as
// request ids, timestamps and nonces are stripped this way so the comparison
// stays byte-stable across runs.
func canonicalJSON(t *testing.T, data []byte, dropKeys ...string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("canonicalJSON: invalid JSON %q: %v", string(data), err)
	}
	drop := make(map[string]bool, len(dropKeys))
	for _, k := range dropKeys {
		drop[k] = true
	}
	normalized, err := json.Marshal(dropKeysDeep(v, drop))
	if err != nil {
		t.Fatalf("canonicalJSON: re-marshal: %v", err)
	}
	return string(normalized)
}

func dropKeysDeep(v any, drop map[string]bool) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if drop[k] {
				continue
			}
			out[k] = dropKeysDeep(child, drop)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = dropKeysDeep(child, drop)
		}
		return out
	default:
		return v
	}
}

// assertJSONFixture compares an observed JSON payload with a stored fixture.
// Both sides are normalised (parsed, volatile keys dropped, keys sorted) and
// only then compared, so a reordered object never fails the test while a
// missing or changed field does.
func assertJSONFixture(t *testing.T, rel string, actual []byte, dropKeys ...string) {
	t.Helper()
	path := filepath.Join(fixturesDir, rel)
	if isTruthyEnv(updateFixturesEnv) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, actual, "", "  "); err != nil {
			t.Fatalf("update fixture %s: invalid JSON: %v", rel, err)
		}
		pretty.WriteByte('\n')
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("update fixture %s: mkdir: %v", rel, err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatalf("update fixture %s: write: %v", rel, err)
		}
		return
	}

	want := canonicalJSON(t, loadFixture(t, rel), dropKeys...)
	got := canonicalJSON(t, actual, dropKeys...)
	if want != got {
		t.Errorf("outbound payload differs from fixture %s\n want: %s\n  got: %s\n\n(re-run with %s=1 to regenerate after an intentional wire change)",
			rel, want, got, updateFixturesEnv)
	}
}

func isTruthyEnv(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Shared request inputs
// ---------------------------------------------------------------------------

// fixtureTools is the tool definition reused by every wire family so the
// fixtures pin how one identical logical tool is serialised per provider.
func fixtureTools() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: &ToolFunctionSchema{
			Name:        "lookup",
			Description: "Look up a record by id",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "record id"},
				},
				"required": []string{"id"},
			},
		},
	}}
}

// fixtureMessages is the conversation shared by the HTTP wire families.
func fixtureMessages() []Message {
	return []Message{
		{Role: "system", Content: "You are a fixture assistant."},
		{Role: "user", Content: "hello fixture"},
	}
}

// jsonBody returns the request body observed by a recording transport.
func jsonBody(t *testing.T, req recordedRequest) []byte {
	t.Helper()
	if len(req.Body) == 0 {
		t.Fatalf("%s %s sent an empty body", req.Method, req.URL)
	}
	return req.Body
}

// ---------------------------------------------------------------------------
// ACP stub agent
// ---------------------------------------------------------------------------

// acpStubLogEnv names the file the in-process ACP stub appends every inbound
// JSON-RPC line to. It deliberately avoids the sensitive GOCLAW*/CLAUDE*/
// ANTHROPIC* prefixes that filterACPEnv strips from ACP subprocesses.
const acpStubLogEnv = "ACPFIXTURE_INBOUND_LOG"

// acpStubSessionID is the session id the stub hands back from session/new.
const acpStubSessionID = "acp-fixture-session"

// acpStubMessage is the subset of a JSON-RPC request the stub reacts to.
type acpStubMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// TestACPStubAgentHelper is the ACP agent stub itself. The provider is spawned
// as a real subprocess pointing at this test binary with
// -test.run=^TestACPStubAgentHelper$; the helper then speaks just enough of the
// ACP JSON-RPC protocol (initialize, session/new, session/prompt) to let the
// real provider code path run, logging every inbound frame for the test to
// assert against. It exits before the testing framework prints anything, so
// stdout stays a clean NDJSON channel.
func TestACPStubAgentHelper(t *testing.T) {
	logPath := os.Getenv(acpStubLogEnv)
	if logPath == "" {
		t.Skip("not running as the ACP fixture stub")
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acp stub: open log: %v\n", err)
		os.Exit(2)
	}
	defer logFile.Close()

	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		if _, err := logFile.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			os.Exit(2)
		}

		var msg acpStubMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Method {
		case "initialize":
			writeACPStubReply(out, msg.ID, map[string]any{
				"protocolVersion": 1,
				"agentInfo":       map[string]any{"name": "acp-fixture", "version": "0.0.1"},
				"agentCapabilities": map[string]any{
					"loadSession": false,
				},
			})
		case "session/new":
			writeACPStubReply(out, msg.ID, map[string]any{"sessionId": acpStubSessionID})
		case "session/prompt":
			writeACPStubNotification(out, "session/update", map[string]any{
				"sessionId": acpStubSessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "fixture-ok"},
				},
			})
			writeACPStubReply(out, msg.ID, map[string]any{"stopReason": "end_turn"})
		case "session/cancel":
			// notification — no reply expected
		default:
			writeACPStubLine(out, map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
	os.Exit(0)
}

func writeACPStubReply(w *bufio.Writer, id *int64, result map[string]any) {
	writeACPStubLine(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeACPStubNotification(w *bufio.Writer, method string, params map[string]any) {
	writeACPStubLine(w, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func writeACPStubLine(w *bufio.Writer, msg map[string]any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
	_ = w.Flush()
}

// acpStubInboundFrames returns every JSON-RPC frame the stub received,
// one JSON object per line.
func acpStubInboundFrames(t *testing.T, logPath string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read ACP stub inbound log: %v", err)
	}
	var frames []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("ACP stub logged a non-JSON frame %q: %v", line, err)
		}
		frames = append(frames, frame)
	}
	return frames
}

// ---------------------------------------------------------------------------
// oauth2 stub token source (Vertex)
// ---------------------------------------------------------------------------

// stubServiceAccountTokenSource is a deterministic GCP service-account token
// source stand-in: the oauth2.Transport under test stamps its token onto the
// Authorization header exactly as it would with a real metadata-server token.
func stubServiceAccountTokenSource(token string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})
}

// rawJSON marshals v without escaping HTML, so fixture diffs stay readable.
func rawJSON(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
