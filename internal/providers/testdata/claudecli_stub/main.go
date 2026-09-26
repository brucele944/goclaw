// Command claudecli_stub is a fake `claude` CLI used by provider tests to
// exercise subprocess failure/retry paths without invoking the real CLI.
//
// Environment:
//
//	GOCLAW_CLAUDE_STUB_MODE    "" (success) | duplicate-session-id | always-duplicate
//	                           | deny-rule-after-resume
//	                           duplicate-session-id and deny-rule-after-resume fail
//	                           only when the args carry --session-id;
//	                           always-duplicate fails every run; deny-rule-after-resume
//	                           additionally rejects one deny rule on the first
//	                           --resume call, forcing the provider's
//	                           --disallowedTools retry to stay resumed.
//	GOCLAW_CLAUDE_STUB_LOG     append one line per invocation (args joined by space)
//	GOCLAW_CLAUDE_STUB_RESULT  assistant result text (default "stub-ok")
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

func main() {
	args := os.Args[1:]

	// priorRuns is the number of invocations before this one; modes that need
	// per-invocation behavior read it instead of keeping separate state.
	logPath := os.Getenv("GOCLAW_CLAUDE_STUB_LOG")
	priorRuns := countLogLines(logPath)
	appendLogLine(logPath, strings.Join(args, " "))

	sessionID := flagValue(args, "--session-id")
	if sessionID == "" {
		sessionID = flagValue(args, "--resume")
	}

	mode := os.Getenv("GOCLAW_CLAUDE_STUB_MODE")
	switch {
	case mode == "always-duplicate",
		(mode == "duplicate-session-id" || mode == "deny-rule-after-resume") && slices.Contains(args, "--session-id"):
		fmt.Fprintf(os.Stderr, "Error: Session ID %s is already in use.\n", sessionID)
		os.Exit(1)
	case mode == "deny-rule-after-resume" && slices.Contains(args, "--resume") && priorRuns == 1:
		fmt.Fprintln(os.Stderr, `Permission deny rule "Bash" matches no known tool`)
		os.Exit(1)
	}

	result := os.Getenv("GOCLAW_CLAUDE_STUB_RESULT")
	if result == "" {
		result = "stub-ok"
	}

	if flagValue(args, "--output-format") == "stream-json" {
		writeJSON(map[string]any{
			"type":    "assistant",
			"message": map[string]any{"content": []map[string]any{{"type": "text", "text": result}}},
		})
		writeJSON(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": result})
		return
	}

	writeJSON(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": false,
		"result":   result,
		"usage":    map[string]any{"input_tokens": 1, "output_tokens": 2},
	})
}

// appendLogLine appends one line to the invocation log (no-op when path is empty).
func appendLogLine(path, line string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	_ = f.Close()
}

// countLogLines returns the number of lines already in the invocation log.
func countLogLines(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// flagValue returns the value of --flag in either "--flag value" or "--flag=value" form.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

func writeJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub cli: marshal:", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}
