package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Chat runs the CLI synchronously and returns the final response.
func (p *ClaudeCLIProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	systemPrompt, userMsg, images := extractFromMessages(req.Messages)
	sessionKey := extractStringOpt(req.Options, OptSessionKey)
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if err := validateCLIModel(model); err != nil {
		return nil, err
	}

	unlock := p.lockSession(sessionKey)
	defer unlock()

	workDir := p.ensureWorkDir(sessionKey)
	if systemPrompt != "" {
		p.writeClaudeMD(workDir, systemPrompt)
	}

	cliSessionID := deriveSessionUUID(sessionKey)
	disableTools := extractBoolOpt(req.Options, OptDisableTools)
	bc := bridgeContextFromOpts(req.Options)
	mcpPath := p.resolveMCPConfigPath(ctx, sessionKey, bc)
	// Claude CLI >= v2.1.87 requires matching input/output formats.
	// When images are present, buildArgs adds --input-format stream-json,
	// so output format must also be stream-json.
	outputFmt := "json"
	if len(images) > 0 {
		outputFmt = "stream-json"
	}
	effortLevel := extractStringOpt(req.Options, OptThinkingLevel)
	allowedToolNames := extractStringSliceOpt(req.Options, OptAllowedToolNames)

	// rebuild re-creates args (and stdin for image turns) for a retry that needs
	// a different session mode.
	var args []string
	var stdin *bytes.Reader
	resumeRetried := false
	rebuild := func(forceResume bool) {
		args = p.buildArgs(model, workDir, mcpPath, cliSessionID, forceResume, outputFmt, len(images) > 0, disableTools, effortLevel, allowedToolNames)
		stdin = nil
		if len(images) > 0 {
			stdin = buildStreamJSONInput(userMsg, images)
		} else {
			args = append(args, "--", userMsg)
		}
	}

	rebuild(false)

	for {
		cmd := exec.CommandContext(ctx, p.cliPath, args...)
		cmd.Dir = workDir
		cmd.Env = filterCLIEnv(os.Environ())
		if effortLevel != "" && effortLevel != "off" {
			// Explicit --effort flag takes precedence; drop env to avoid ambiguity.
			cmd.Env = removeEnvKey(cmd.Env, "CLAUDE_CODE_EFFORT_LEVEL")
		}
		if stdin != nil {
			cmd.Stdin = stdin
		}

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		slog.Debug("claude-cli exec", "cmd", fmt.Sprintf("%s %s", p.cliPath, strings.Join(args, " ")), "workdir", workDir)
		output, err := cmd.Output()
		if err != nil {
			if tool, retry := p.noteInvalidDisallowedTool(stderr.String()); retry {
				slog.Warn("claude-cli: retrying without unsupported --disallowedTools rule", "tool", tool)
				// Keep the resume decision: rebuilding from scratch would send
				// --session-id again and re-trigger the duplicate-session error.
				rebuild(resumeRetried)
				continue
			}
			// The CLI's own store already owns this session ID (our file lookup
			// disagreed with it) — resume instead of failing the turn.
			if !resumeRetried && sessionIDInUse(stderr.String()) {
				resumeRetried = true
				slog.Warn("claude-cli: session ID already exists in CLI store, retrying with --resume", "session_id", cliSessionID.String())
				rebuild(true)
				continue
			}
			return nil, fmt.Errorf("claude-cli: %w%s", err, cliFailureSuffix(output, stderr.String()))
		}

		resp, parseErr := parseJSONResponse(output)
		if parseErr != nil {
			// The CLI can exit 0 while reporting the failure only in its JSON
			// events (no content to parse) — surface that reason instead of a
			// bare "empty response".
			if reason := cliFailureReason(output); reason != "" {
				return nil, fmt.Errorf("claude-cli: %s", reason)
			}
			return nil, parseErr
		}
		return resp, nil
	}
}

// cliFailureSuffix renders the failure detail appended to a Chat error.
// The structured stdout reason wins over stderr because stderr is frequently
// empty for API/quota failures (see cliFailureReason).
func cliFailureSuffix(output []byte, stderr string) string {
	parts := make([]string, 0, 2)
	if reason := cliFailureReason(output); reason != "" {
		parts = append(parts, "stdout: "+reason)
	}
	if s := strings.TrimSpace(stderr); s != "" {
		parts = append(parts, "stderr: "+s)
	}
	if len(parts) == 0 {
		return " (no output)"
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// ChatStream runs the CLI with stream-json output, calling onChunk for each text delta.
func (p *ClaudeCLIProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	systemPrompt, userMsg, images := extractFromMessages(req.Messages)
	sessionKey := extractStringOpt(req.Options, OptSessionKey)
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if err := validateCLIModel(model); err != nil {
		return nil, err
	}

	slog.Debug("claude-cli: acquiring session lock", "session_key", sessionKey)
	unlock := p.lockSession(sessionKey)
	slog.Debug("claude-cli: session lock acquired", "session_key", sessionKey)
	defer func() {
		unlock()
		slog.Debug("claude-cli: session lock released", "session_key", sessionKey)
	}()

	workDir := p.ensureWorkDir(sessionKey)
	if systemPrompt != "" {
		p.writeClaudeMD(workDir, systemPrompt)
	}

	cliSessionID := deriveSessionUUID(sessionKey)
	disableTools := extractBoolOpt(req.Options, OptDisableTools)
	bc := bridgeContextFromOpts(req.Options)
	mcpPath := p.resolveMCPConfigPath(ctx, sessionKey, bc)
	effortLevel := extractStringOpt(req.Options, OptThinkingLevel)
	allowedToolNames := extractStringSliceOpt(req.Options, OptAllowedToolNames)

	// rebuild re-creates args (and stdin for image turns) for a retry that needs
	// a different session mode.
	var args []string
	var stdin *bytes.Reader
	resumeRetried := false
	rebuild := func(forceResume bool) {
		args = p.buildArgs(model, workDir, mcpPath, cliSessionID, forceResume, "stream-json", len(images) > 0, disableTools, effortLevel, allowedToolNames)
		stdin = nil
		if len(images) > 0 {
			stdin = buildStreamJSONInput(userMsg, images)
		} else {
			args = append(args, "--", userMsg)
		}
	}

	rebuild(false)

	for {
		cmd := exec.CommandContext(ctx, p.cliPath, args...)
		cmd.WaitDelay = 5 * time.Second // force-close pipes if process lingers after kill
		cmd.Dir = workDir
		cmd.Env = filterCLIEnv(os.Environ())
		if effortLevel != "" && effortLevel != "off" {
			cmd.Env = removeEnvKey(cmd.Env, "CLAUDE_CODE_EFFORT_LEVEL")
		}
		if stdin != nil {
			cmd.Stdin = stdin
		}

		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("claude-cli stdout pipe: %w", err)
		}

		fullCmd := fmt.Sprintf("%s %s", p.cliPath, strings.Join(args, " "))
		slog.Debug("claude-cli stream exec", "cmd", fullCmd, "workdir", workDir)
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("claude-cli start: %w", err)
		}

		// Debug log file: only enabled when GOCLAW_DEBUG=1
		var debugFile *os.File
		if os.Getenv("GOCLAW_DEBUG") == "1" {
			debugLogPath := filepath.Join(workDir, "cli-debug.log")
			debugFile, _ = os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if debugFile != nil {
				fmt.Fprintf(debugFile, "=== CMD: %s\n=== WORKDIR: %s\n=== TIME: %s\n\n", fullCmd, workDir, time.Now().Format(time.RFC3339))
				defer debugFile.Close()
			}
		}

		// Parse stream-json line-by-line
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, StdioScanBufInit), StdioScanBufMax)

		var finalResp ChatResponse
		var contentBuf strings.Builder
		var streamErrMsg string // error message from stream-json result event

		for scanner.Scan() {
			if ctx.Err() != nil {
				break // context cancelled (abort) → exit immediately
			}
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Write raw line to debug log
			if debugFile != nil {
				fmt.Fprintf(debugFile, "%s\n", line)
			}

			var ev cliStreamEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				slog.Debug("claude-cli: skip malformed stream line", "error", err)
				continue
			}

			switch ev.Type {
			case "assistant":
				if ev.Message == nil {
					continue
				}
				text, thinking := extractStreamContent(ev.Message)
				if text != "" {
					contentBuf.WriteString(text)
					onChunk(StreamChunk{Content: text})
				}
				if thinking != "" {
					onChunk(StreamChunk{Thinking: thinking})
				}

			case "result":
				if ev.Result != "" {
					finalResp.Content = ev.Result
				} else {
					finalResp.Content = contentBuf.String()
				}
				finalResp.FinishReason = "stop"
				if ev.Subtype == "error" || ev.IsError {
					finalResp.FinishReason = "error"
					// Prefer ev.Error (result may be empty for usage/rate limit errors).
					if ev.Error != "" {
						streamErrMsg = ev.Error
					} else if ev.Result != "" {
						streamErrMsg = ev.Result
					}
				}
				if ev.Usage != nil {
					finalResp.Usage = &Usage{
						PromptTokens:     ev.Usage.InputTokens,
						CompletionTokens: ev.Usage.OutputTokens,
						TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
					}
				}
			}
		}

		// Context cancelled (abort): best-effort reap (bounded by WaitDelay), then return.
		if ctx.Err() != nil {
			_ = cmd.Wait()
			return nil, ctx.Err()
		}

		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("claude-cli: stream read error: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			if debugFile != nil {
				fmt.Fprintf(debugFile, "\n=== STDERR:\n%s\n=== EXIT ERROR: %v\n", stderrBuf.String(), err)
			}
			// If we got partial content, return it with the error.
			if finalResp.Content != "" {
				return &finalResp, nil
			}
			stderrStr := strings.TrimSpace(stderrBuf.String())
			if tool, retry := p.noteInvalidDisallowedTool(stderrStr); retry {
				slog.Warn("claude-cli: retrying without unsupported --disallowedTools rule", "tool", tool)
				// Keep the resume decision: rebuilding from scratch would send
				// --session-id again and re-trigger the duplicate-session error.
				rebuild(resumeRetried)
				continue
			}
			// The CLI's own store already owns this session ID (our file lookup
			// disagreed with it) — resume instead of failing the turn.
			if !resumeRetried && sessionIDInUse(stderrStr) {
				resumeRetried = true
				slog.Warn("claude-cli: session ID already exists in CLI store, retrying with --resume", "session_id", cliSessionID.String())
				rebuild(true)
				continue
			}
			if stderrStr == "" && finalResp.FinishReason == "error" {
				// claude-cli communicates API errors via stream-json stdout, not stderr.
				if streamErrMsg != "" {
					stderrStr = "stream: " + streamErrMsg
				} else {
					stderrStr = "stream error (no message)"
				}
			}
			return nil, fmt.Errorf("claude-cli: %w (stderr: %s)", err, stderrStr)
		}
		if debugFile != nil && stderrBuf.Len() > 0 {
			fmt.Fprintf(debugFile, "\n=== STDERR:\n%s\n", stderrBuf.String())
		}

		// Fallback if no "result" event was received
		if finalResp.Content == "" {
			finalResp.Content = contentBuf.String()
			finalResp.FinishReason = "stop"
		}

		onChunk(StreamChunk{Done: true})
		return &finalResp, nil
	}
}
