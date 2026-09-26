package providers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	stubBuildOnce sync.Once
	stubCLIPath   string
	stubBuildErr  error
)

// TestMain removes the compiled stub CLI directory after the package's tests run,
// so repeated `go test` runs do not accumulate build dirs in the temp directory.
func TestMain(m *testing.M) {
	code := m.Run()
	if stubCLIPath != "" {
		os.RemoveAll(filepath.Dir(stubCLIPath))
	}
	os.Exit(code)
}

// buildStubCLI compiles testdata/claudecli_stub once per test binary run and
// returns the binary path. The stub lets these tests drive the real exec/retry
// code path without invoking the actual Claude CLI (no API calls, no tokens).
func buildStubCLI(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}
	stubBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "goclaw-claudecli-stub")
		if err != nil {
			stubBuildErr = err
			return
		}
		bin := filepath.Join(dir, "claude-stub")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", bin, "./testdata/claudecli_stub").CombinedOutput()
		if err != nil {
			stubBuildErr = fmt.Errorf("build stub CLI: %v: %s", err, out)
			return
		}
		stubCLIPath = bin
	})
	if stubBuildErr != nil {
		t.Fatal(stubBuildErr)
	}
	return stubCLIPath
}

// stubInvocations returns the args of each stub CLI invocation, one entry per run.
func stubInvocations(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stub invocation log: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestChatRetriesWithResumeOnDuplicateSessionID covers the reported production
// failure: our session-file lookup disagrees with the CLI's store, so the first
// attempt sends --session-id for an ID the CLI already owns. The provider must
// retry once with --resume and return the CLI's answer instead of failing the turn.
func TestChatRetriesWithResumeOnDuplicateSessionID(t *testing.T) {
	stub := buildStubCLI(t)
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_MODE", "duplicate-session-id")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(t.TempDir()))
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Options:  map[string]any{OptSessionKey: "duplicate-session-recovery"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("content = %q, want %q", resp.Content, "stub-ok")
	}

	invocations := stubInvocations(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want 2: %v", len(invocations), invocations)
	}
	if !strings.Contains(invocations[0], "--session-id") {
		t.Errorf("first invocation must create the session, got %q", invocations[0])
	}
	if !strings.Contains(invocations[1], "--resume") {
		t.Errorf("second invocation must resume, got %q", invocations[1])
	}
}

// TestChatDuplicateSessionIDRetryIsBounded guards the retry loop: a CLI that keeps
// refusing the session ID must produce an error after one retry, never spin.
func TestChatDuplicateSessionIDRetryIsBounded(t *testing.T) {
	stub := buildStubCLI(t)
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_MODE", "always-duplicate")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(t.TempDir()))
	_, err := p.Chat(ctx, ChatRequest{
		Model:    "sonnet",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Options:  map[string]any{OptSessionKey: "duplicate-session-bounded"},
	})
	if err == nil {
		t.Fatal("expected an error when the CLI keeps reporting a duplicate session ID")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error = %v, want it to surface the CLI's duplicate-session message", err)
	}
	if invocations := stubInvocations(t, logPath); len(invocations) != 2 {
		t.Fatalf("invocations = %d, want exactly 2 (one retry, no loop): %v", len(invocations), invocations)
	}
}

// TestChatKeepsResumeAfterDisallowedToolsRetry pins the interleaving of the two
// retry branches in Chat: after a duplicate-session retry, a rejected
// --disallowedTools rule must rebuild with --resume still forced. Rebuilding with
// a hardcoded false would send --session-id again and re-trigger "already in use".
func TestChatKeepsResumeAfterDisallowedToolsRetry(t *testing.T) {
	stub := buildStubCLI(t)
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_MODE", "deny-rule-after-resume")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(t.TempDir()))
	resp, err := p.Chat(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Options:  map[string]any{OptSessionKey: "resume-then-deny-rule"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("content = %q, want %q", resp.Content, "stub-ok")
	}

	invocations := stubInvocations(t, logPath)
	if len(invocations) != 3 {
		t.Fatalf("invocations = %d, want 3 (session-id, resume+deny rule, resume): %v", len(invocations), invocations)
	}
	if !strings.Contains(invocations[2], "--resume") {
		t.Errorf("third invocation must stay resumed, got %q", invocations[2])
	}
	if strings.Contains(invocations[2], "Bash") {
		t.Errorf("third invocation must drop the rejected deny rule, got %q", invocations[2])
	}
}

// TestChatStreamKeepsResumeAfterDisallowedToolsRetry is the ChatStream counterpart
// of the interleaving guard above.
func TestChatStreamKeepsResumeAfterDisallowedToolsRetry(t *testing.T) {
	stub := buildStubCLI(t)
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_MODE", "deny-rule-after-resume")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(t.TempDir()))
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Model:    "sonnet",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Options:  map[string]any{OptSessionKey: "resume-then-deny-rule-stream"},
	}, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("content = %q, want %q", resp.Content, "stub-ok")
	}

	invocations := stubInvocations(t, logPath)
	if len(invocations) != 3 {
		t.Fatalf("invocations = %d, want 3 (session-id, resume+deny rule, resume): %v", len(invocations), invocations)
	}
	if !strings.Contains(invocations[2], "--resume") {
		t.Errorf("third invocation must stay resumed, got %q", invocations[2])
	}
}

// TestChatStreamRetriesWithResumeOnDuplicateSessionID covers the streaming path and
// the image branch: the retry must rebuild stdin and keep --input-format stream-json.
func TestChatStreamRetriesWithResumeOnDuplicateSessionID(t *testing.T) {
	stub := buildStubCLI(t)
	logPath := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("GOCLAW_CLAUDE_STUB_MODE", "duplicate-session-id")
	t.Setenv("GOCLAW_CLAUDE_STUB_LOG", logPath)

	p := NewClaudeCLIProvider(stub, WithClaudeCLIWorkDir(t.TempDir()))
	resp, err := p.ChatStream(context.Background(), ChatRequest{
		Model: "sonnet",
		Messages: []Message{{
			Role:    "user",
			Content: "look at this",
			Images:  []ImageContent{{MimeType: "image/png", Data: "aGk="}},
		}},
		Options: map[string]any{OptSessionKey: "duplicate-session-stream"},
	}, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "stub-ok" {
		t.Fatalf("content = %q, want %q", resp.Content, "stub-ok")
	}

	invocations := stubInvocations(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want 2: %v", len(invocations), invocations)
	}
	for i, inv := range invocations {
		if !strings.Contains(inv, "--input-format stream-json") {
			t.Errorf("invocation %d must keep stream-json input for image turns, got %q", i, inv)
		}
	}
	if !strings.Contains(invocations[1], "--resume") {
		t.Errorf("second invocation must resume, got %q", invocations[1])
	}
}
