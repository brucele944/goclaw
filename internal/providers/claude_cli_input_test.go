package providers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// decodeStreamInput reads a stream-json stdin payload and returns its text blocks.
func decodeStreamInput(t *testing.T, r io.Reader) []string {
	t.Helper()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	var msg struct {
		Message struct {
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("parse stream-json: %v\nraw: %s", err, raw)
	}
	var texts []string
	for _, block := range msg.Message.Content {
		if block["type"] == "text" {
			s, _ := block["text"].(string)
			texts = append(texts, s)
		}
	}
	return texts
}

// containsArg lives in request_fixtures_test.go (same package).

// Regression guard for the Telegram outage of 2026-09-28: a user message larger
// than the OS command-line limit was appended to argv, so the claude CLI could
// not be spawned at all ("The filename or extension is too long"), compaction
// failed, and the agent run aborted without replying.
func TestCLIInvocationInput_OversizedMessageGoesToStdin(t *testing.T) {
	p := NewClaudeCLIProvider("claude")
	big := strings.Repeat("x", cliArgvMessageLimit+1)
	base := []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json"}

	args, stdin := p.cliInvocationInput(base, big, nil)

	if containsArg(args, big) {
		t.Fatal("oversized message must not travel in argv")
	}
	if containsArg(args, "--") {
		t.Fatal("argv prompt separator must not be added when the message goes to stdin")
	}
	if len(args) != len(base) {
		t.Fatalf("args changed for the stdin path: %v", args)
	}
	if stdin == nil {
		t.Fatal("stdin payload must be set for an oversized message")
	}
	texts := decodeStreamInput(t, stdin)
	if len(texts) != 1 || len(texts[0]) != len(big) {
		t.Fatalf("stdin text blocks = %d, first block len = %d; want 1 block of %d (no truncation)",
			len(texts), len(texts[0]), len(big))
	}
}

// Small text messages keep the argv path — the long-standing behaviour.
func TestCLIInvocationInput_SmallMessageStaysInArgv(t *testing.T) {
	p := NewClaudeCLIProvider("claude")
	base := []string{"-p", "--output-format", "json"}

	args, stdin := p.cliInvocationInput(base, "hello", nil)

	if stdin != nil {
		t.Fatal("stdin must stay unset when the message fits in argv")
	}
	if len(args) != len(base)+2 || args[len(base)] != "--" || args[len(base)+1] != "hello" {
		t.Fatalf("args = %v; want message appended after -- separator", args)
	}
}

// Images still force the stdin path (pre-existing behaviour).
func TestCLIInvocationInput_ImagesGoToStdin(t *testing.T) {
	p := NewClaudeCLIProvider("claude")
	images := []ImageContent{{MimeType: "image/png", Data: "abc"}}

	args, stdin := p.cliInvocationInput([]string{"-p"}, "describe", images)

	if stdin == nil {
		t.Fatal("images must be delivered through stdin")
	}
	if containsArg(args, "describe") {
		t.Fatal("message must not travel in argv when images force the stdin path")
	}
	if texts := decodeStreamInput(t, stdin); len(texts) != 1 || texts[0] != "describe" {
		t.Fatalf("stdin text = %v; want [describe]", texts)
	}
}

// The routing threshold is inclusive: the limit itself still fits, one byte over
// does not.
func TestCLIUseStreamInput_AtLimit(t *testing.T) {
	if cliUseStreamInput(strings.Repeat("x", cliArgvMessageLimit), nil) {
		t.Fatalf("message of exactly %d chars must stay in argv", cliArgvMessageLimit)
	}
	if !cliUseStreamInput(strings.Repeat("x", cliArgvMessageLimit+1), nil) {
		t.Fatalf("message above %d chars must go to stdin", cliArgvMessageLimit)
	}
	if !cliUseStreamInput("hi", []ImageContent{{MimeType: "image/png"}}) {
		t.Fatal("images must go to stdin regardless of message size")
	}
}

// The CLI rejects stdin input unless --input-format matches, so the flag must be
// emitted exactly when the message is routed to stdin.
func TestBuildArgs_InputFormatMatchesStreamRouting(t *testing.T) {
	p := NewClaudeCLIProvider("claude")
	workDir := t.TempDir()
	id := uuid.New()

	withStdin := p.buildArgs("sonnet", workDir, "", id, false, "stream-json", true, false, "", nil)
	if !containsArg(withStdin, "--input-format") {
		t.Fatalf("args = %v; want --input-format for the stdin path", withStdin)
	}
	argvPath := p.buildArgs("sonnet", workDir, "", id, false, "json", false, false, "", nil)
	if containsArg(argvPath, "--input-format") {
		t.Fatalf("args = %v; must not carry --input-format on the argv path", argvPath)
	}
	for _, args := range [][]string{withStdin, argvPath} {
		if !containsArg(args, "--output-format") {
			t.Fatalf("args = %v; want --output-format", args)
		}
	}
}
