package providers

import (
	"strings"
	"testing"
)

// The CLI reports API/quota/auth failures in its JSON events while stderr stays
// empty (verified live against Claude Code: a Pro subscription hitting its
// session limit exits 1 with a real message only in the "result" event). Before
// this, Chat surfaced just `exit status 1 (stderr: )` and the cause was invisible.
func TestCLIFailureReason(t *testing.T) {
	// Verbatim shape captured from `claude -p --output-format json` when the
	// subscription hit its session limit.
	sessionLimit := `[{"type":"rate_limit_event","rate_limit_info":{}},
		{"type":"assistant","error":"rate_limit"},
		{"type":"result","subtype":"success","is_error":true,"num_turns":1,
		 "result":"You've hit your session limit · resets 2:10am (Asia/Ho_Chi_Minh)"}]`

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "array: is_error with subtype success (quota)",
			input: sessionLimit,
			want:  "You've hit your session limit · resets 2:10am (Asia/Ho_Chi_Minh)",
		},
		{
			name:  "array: subtype error with empty result falls back to error field",
			input: `[{"type":"result","subtype":"error","result":"","error":"api_error"}]`,
			want:  "api_error",
		},
		{
			name:  "array: only assistant error code, no result event",
			input: `[{"type":"assistant","error":"rate_limit"}]`,
			want:  "rate_limit",
		},
		{
			name:  "ndjson: single result line (stream-json)",
			input: "{\"type\":\"system\",\"subtype\":\"init\"}\n" +
				"{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":true,\"result\":\"session limit reached\"}\n",
			want: "session limit reached",
		},
		{
			name:  "success result is not a failure reason",
			input: `[{"type":"result","subtype":"success","result":"Hello there"}]`,
			want:  "",
		},
		{
			name:  "plain text output",
			input: "Command failed: unknown flag --nope",
			want:  "",
		},
		{
			name:  "empty output",
			input: "",
			want:  "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cliFailureReason([]byte(c.input)); got != c.want {
				t.Errorf("cliFailureReason() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCLIFailureSuffix(t *testing.T) {
	quotaOut := `[{"type":"result","subtype":"success","is_error":true,"result":"session limit reached"}]`

	cases := []struct {
		name   string
		output string
		stderr string
		want   string
	}{
		{
			name:   "stdout reason wins when stderr is empty",
			output: quotaOut,
			stderr: "",
			want:   " (stdout: session limit reached)",
		},
		{
			name:   "stdout reason plus stderr diagnostic",
			output: quotaOut,
			stderr: "[mcp-sdk] SEP-2352 warning\n",
			want:   " (stdout: session limit reached; stderr: [mcp-sdk] SEP-2352 warning)",
		},
		{
			name:   "stderr only keeps the previous message shape",
			output: "not json",
			stderr: "boom",
			want:   " (stderr: boom)",
		},
		{
			name:   "neither stream carries a reason",
			output: "",
			stderr: "  ",
			want:   " (no output)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cliFailureSuffix([]byte(c.output), c.stderr)
			if got != c.want {
				t.Errorf("cliFailureSuffix() = %q, want %q", got, c.want)
			}
		})
	}
}

// A quota/auth error must never be handed back as a normal answer: the CLI keeps
// subtype="success" in that case, so only is_error reveals it.
func TestParseJSONResponseFlagsIsError(t *testing.T) {
	arrayOut := []byte(`[{"type":"result","subtype":"success","is_error":true,"result":"You've hit your session limit"}]`)
	resp, err := parseJSONResponse(arrayOut)
	if err != nil {
		t.Fatalf("parseJSONResponse: %v", err)
	}
	if resp.FinishReason != "error" {
		t.Errorf("array output: FinishReason = %q, want %q", resp.FinishReason, "error")
	}

	lineOut := []byte("{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":true,\"result\":\"You've hit your session limit\"}\n")
	resp, err = parseJSONResponse(lineOut)
	if err != nil {
		t.Fatalf("parseJSONResponse (line): %v", err)
	}
	if resp.FinishReason != "error" {
		t.Errorf("line output: FinishReason = %q, want %q", resp.FinishReason, "error")
	}

	okOut := []byte(`[{"type":"result","subtype":"success","result":"Hello"}]`)
	resp, err = parseJSONResponse(okOut)
	if err != nil {
		t.Fatalf("parseJSONResponse (ok): %v", err)
	}
	if resp.FinishReason != "stop" || !strings.Contains(resp.Content, "Hello") {
		t.Errorf("success output: FinishReason = %q content = %q, want stop/Hello", resp.FinishReason, resp.Content)
	}
}
