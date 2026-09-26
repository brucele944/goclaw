package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// parseJSONResponse parses the CLI JSON output into a ChatResponse.
func parseJSONResponse(data []byte) (*ChatResponse, error) {
	// Try parsing as JSON array first (CLI may output all events as a single array).
	if resp := parseJSONArray(data); resp != nil {
		return resp, nil
	}

	// Fallback: CLI may output one JSON object per line.
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if resp := parseSingleJSONResult(line); resp != nil {
			return resp, nil
		}
	}

	// Last resort: treat entire output as text response
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("claude-cli: empty response")
	}
	return &ChatResponse{
		Content:      trimmed,
		FinishReason: "stop",
	}, nil
}

// parseJSONArray tries to parse data as a JSON array of CLI events, extracting
// the "result" event's text content and "assistant" event's text blocks.
func parseJSONArray(data []byte) *ChatResponse {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}

	var events []json.RawMessage
	if err := json.Unmarshal(trimmed, &events); err != nil {
		return nil
	}

	var resultText string
	var assistantText strings.Builder
	var usage *Usage
	finishReason := "stop"

	for _, raw := range events {
		var ev struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype,omitempty"`
			Result  string          `json:"result,omitempty"`
			Message json.RawMessage `json:"message,omitempty"`
			Usage   *cliUsage       `json:"usage,omitempty"`
			IsError bool            `json:"is_error,omitempty"`
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "result":
			resultText = ev.Result
			// is_error is authoritative: quota/auth/API failures set it even
			// when subtype stays "success", so subtype alone would let an
			// error message pass as a legitimate answer.
			if ev.Subtype == "error" || ev.IsError {
				finishReason = "error"
			}
			if ev.Usage != nil {
				usage = &Usage{
					PromptTokens:     ev.Usage.InputTokens,
					CompletionTokens: ev.Usage.OutputTokens,
					TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
				}
			}

		case "assistant":
			// Extract text from content blocks
			if ev.Message != nil {
				var msg cliStreamMsg
				if err := json.Unmarshal(ev.Message, &msg); err == nil {
					for _, block := range msg.Content {
						if block.Type == "text" {
							assistantText.WriteString(block.Text)
						}
					}
				}
			}
		}
	}

	// Prefer "result" text, fall back to concatenated assistant text blocks
	content := resultText
	if content == "" {
		content = assistantText.String()
	}
	if content == "" {
		return nil
	}

	return &ChatResponse{
		Content:      content,
		FinishReason: finishReason,
		Usage:        usage,
	}
}

// parseSingleJSONResult tries to parse a single JSON line as a "result" event.
func parseSingleJSONResult(line []byte) *ChatResponse {
	var resp cliJSONResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil
	}
	if resp.Type != "result" {
		return nil
	}
	cr := &ChatResponse{
		Content:      resp.Result,
		FinishReason: "stop",
	}
	if resp.Subtype == "error" || resp.IsError {
		cr.FinishReason = "error"
	}
	if resp.Usage != nil {
		cr.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}
	return cr
}

// cliFailureReason extracts the human-readable failure reason from the Claude
// CLI's stdout.
//
// The CLI reports API, quota and auth failures inside its JSON events — a
// "result" event carrying is_error (subtype may still read "success") or an
// "error" code on an assistant event — while stderr is frequently empty. A
// non-zero exit status alone therefore renders the failure opaque, e.g. a Pro
// subscription hitting its session limit prints:
//
//	{"type":"result","subtype":"success","is_error":true,
//	 "result":"You've hit your session limit · resets 2:10am (Asia/Ho_Chi_Minh)"}
//
// Handles both the JSON-array shape (--output-format json) and the
// one-object-per-line shape (--output-format stream-json). Returns "" when the
// output carries no structured reason (plain text, unrelated JSON, success).
func cliFailureReason(output []byte) string {
	var resultReason, eventErr string

	consider := func(raw []byte) {
		var ev cliStreamEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return
		}
		switch ev.Type {
		case "result":
			if !ev.IsError && ev.Subtype != "error" {
				return
			}
			if reason := strings.TrimSpace(ev.Result); reason != "" {
				resultReason = reason
				return
			}
			if reason := strings.TrimSpace(ev.Error); reason != "" {
				resultReason = reason
			}
		case "assistant":
			if eventErr == "" {
				eventErr = strings.TrimSpace(ev.Error)
			}
		}
	}

	trimmed := bytes.TrimSpace(output)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var events []json.RawMessage
		if err := json.Unmarshal(trimmed, &events); err == nil {
			for _, raw := range events {
				consider(raw)
			}
		}
	} else {
		for line := range bytes.SplitSeq(trimmed, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				consider(line)
			}
		}
	}

	if resultReason != "" {
		return resultReason
	}
	return eventErr
}

// extractStreamContent extracts text and thinking from a stream message.
func extractStreamContent(msg *cliStreamMsg) (text, thinking string) {
	var textBuf, thinkBuf strings.Builder
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			textBuf.WriteString(block.Text)
		case "thinking":
			thinkBuf.WriteString(block.Thinking)
		}
	}
	return textBuf.String(), thinkBuf.String()
}
