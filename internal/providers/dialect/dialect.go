// Package dialect converts the in-band, text-protocol tool-call formats some
// models emit into structured tool calls.
//
// A model that was fine-tuned on a text protocol (Qwen3, DeepSeek-V3, Kimi-K2,
// GLM, Hermes/XML, harmony) does not use the OpenAI `tool_calls` array. It writes
// the call into the assistant text — sometimes wrapped in its own markup, which
// then leaks into the user-visible answer if nothing repairs it.
//
// One Converter per family owns three things: how tools are described in the
// prompt (InjectPrompt), how a streaming text delta is scanned for calls
// (ScanStream, incremental — a call may span chunks), and how leaked markup is
// removed from visible text (Heal).
//
// Selection: GOCLAW_TOOL_DIALECT (global escape hatch) → llm_models.compat
// .tool_dialect → the per-wire default. The zero selection means "the wire
// protocol carries tool calls natively" and no converter runs.
package dialect

import (
	"os"
	"sort"
	"strings"
)

// Tool is a tool as the converter sees it. Declared here (not imported from the
// providers package) so providers can depend on this package without a cycle.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is one parsed call.
type ToolCall struct {
	ID         string
	Name       string
	Arguments  map[string]any
	ParseError string
}

// State is the per-stream scanner state. A converter must be able to resume from
// it: deltas arrive in pieces and a call may span several of them.
type State struct {
	buf   string
	index int
}

// Converter is one model family's in-band tool protocol.
type Converter interface {
	// Name is the dialect id used in declarations and GOCLAW_TOOL_DIALECT.
	Name() string
	// InjectPrompt appends the in-band tool description to the system prompt.
	InjectPrompt(system string, tools []Tool) string
	// ScanStream consumes one text delta and returns the visible content plus any
	// calls completed by this delta.
	ScanStream(delta string, st *State) (content string, calls []ToolCall, err error)
	// Heal removes leaked tool markup from a completed message.
	Heal(markup string) string
}

// Names lists the registered dialect ids, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// For returns the converter for a dialect id. Aliases are normalised first
// ("xml" → "hermes/xml").
func For(name string) (Converter, bool) {
	c, ok := registry[normalize(name)]
	return c, ok
}

// EnvOverrideVar is the global escape hatch, mirroring OMP's PI_DIALECT: when
// set to a registered dialect it wins for every model.
const EnvOverrideVar = "GOCLAW_TOOL_DIALECT"

// Select chooses the converter for a wire API and a declared dialect. An empty
// result means the wire protocol carries tool calls natively (no converter).
func Select(wireAPI, declared string) Converter {
	if env := strings.TrimSpace(os.Getenv(EnvOverrideVar)); env != "" {
		if c, ok := For(env); ok {
			return c
		}
	}
	if c, ok := For(declared); ok {
		return c
	}
	if wireAPI != "" {
		if name, ok := wireDefaults[strings.ToLower(wireAPI)]; ok {
			if c, ok := For(name); ok {
				return c
			}
		}
	}
	return nil
}

// wireDefaults is the per-wire default dialect. Everything GoClaw speaks today
// carries tool calls natively, so the table is intentionally empty; it exists so
// a future text-only wire can declare its protocol in one place.
var wireDefaults = map[string]string{}

// normalize maps user-facing aliases onto canonical ids.
func normalize(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none", "off", "native":
		return ""
	case "xml", "hermes", "hermes_xml":
		return "hermes/xml"
	case "deepseek", "deepseek_v3":
		return "deepseek-v3"
	case "kimi", "kimi_k2":
		return "kimi-k2"
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// registry is populated by init in converters.go.
var registry = map[string]Converter{}

func register(c Converter) { registry[c.Name()] = c }
