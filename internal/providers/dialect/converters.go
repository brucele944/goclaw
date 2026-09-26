package dialect

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func init() {
	register(newMarker(xmlHermes))
	register(newMarker(qwen3))
	register(newMarker(glm))
	register(newMarker(deepseekV3))
	register(newMarker(kimiK2))
	register(newMarker(harmony))
}

// callsSectionMarker pairs are section wrappers that carry no call data, only
// grouping. They are stripped from visible content and during healing.
var sectionMarkers = map[string][]string{
	"deepseek-v3": {"<|tool▁calls▁begin|>", "<|tool▁calls▁end|>"},
	"kimi-k2":     {"<|tool_calls_section_begin|>", "<|tool_calls_section_end|>"},
}

// familySpec declares one family's in-band protocol.
type familySpec struct {
	name  string
	start string
	end   string
	parse func(payload string) ([]ToolCall, error)
	// describe renders the in-band tool instructions.
	describe func(tools []Tool) string
}

var (
	xmlHermes = familySpec{
		name:     "hermes/xml",
		start:    "<tool_call>",
		end:      "</tool_call>",
		parse:    parseHermesPayload,
		describe: describeXML,
	}
	qwen3 = familySpec{
		name:     "qwen3",
		start:    "<tool_call>",
		end:      "</tool_call>",
		parse:    parseQwenPayload,
		describe: describeXML,
	}
	glm = familySpec{
		name:     "glm",
		start:    "<tool_call>",
		end:      "</tool_call>",
		parse:    parseGLMPayload,
		describe: describeGLM,
	}
	deepseekV3 = familySpec{
		name:     "deepseek-v3",
		start:    "<|tool▁call▁begin|>",
		end:      "<|tool▁call▁end|>",
		parse:    parseDeepSeekPayload,
		describe: describeFenced,
	}
	kimiK2 = familySpec{
		name:     "kimi-k2",
		start:    "<|tool_call_begin|>",
		end:      "<|tool_call_end|>",
		parse:    parseKimiPayload,
		describe: describeKimi,
	}
	harmony = familySpec{
		name:     "harmony",
		start:    "<|channel|>commentary to=",
		end:      "<|call|>",
		parse:    parseHarmonyPayload,
		describe: describeHarmony,
	}
)

type markerConverter struct{ spec familySpec }

func newMarker(spec familySpec) *markerConverter { return &markerConverter{spec: spec} }

func (c *markerConverter) Name() string { return c.spec.name }

func (c *markerConverter) InjectPrompt(system string, tools []Tool) string {
	if len(tools) == 0 || c.spec.describe == nil {
		return system
	}
	block := c.spec.describe(tools)
	if block == "" {
		return system
	}
	if strings.TrimSpace(system) == "" {
		return block
	}
	return strings.TrimRight(system, "\n") + "\n\n" + block
}

// ScanStream extracts complete calls from the buffered text. Text before a start
// marker is returned as content; a start marker split across deltas is held back
// until the next delta completes (or disproves) it.
func (c *markerConverter) ScanStream(delta string, st *State) (string, []ToolCall, error) {
	if st == nil {
		return delta, nil, nil
	}
	st.buf += delta
	var (
		out   strings.Builder
		calls []ToolCall
	)
	for {
		start := strings.Index(st.buf, c.spec.start)
		if start < 0 {
			hold := partialMarkerSuffix(st.buf, c.spec.start)
			out.WriteString(stripMarkers(st.buf[:len(st.buf)-hold], c.noise()))
			st.buf = st.buf[len(st.buf)-hold:]
			break
		}
		out.WriteString(stripMarkers(st.buf[:start], c.noise()))
		rest := st.buf[start+len(c.spec.start):]
		end := strings.Index(rest, c.spec.end)
		if end < 0 {
			// Incomplete call: keep it buffered for the next delta.
			st.buf = st.buf[start:]
			break
		}
		payload := rest[:end]
		st.buf = rest[end+len(c.spec.end):]
		parsed, err := c.spec.parse(payload)
		if err != nil {
			return out.String(), calls, err
		}
		for i := range parsed {
			if parsed[i].ID == "" {
				parsed[i].ID = fmt.Sprintf("%s_%d_%d", c.spec.name, st.index, i)
			}
		}
		st.index++
		calls = append(calls, parsed...)
	}
	return out.String(), calls, nil
}

// Heal removes complete call blocks and section markers, and truncates a
// dangling start marker (an unterminated block is never safe to show).
func (c *markerConverter) Heal(markup string) string {
	if markup == "" {
		return markup
	}
	var out strings.Builder
	rest := markup
	for {
		start := strings.Index(rest, c.spec.start)
		if start < 0 {
			out.WriteString(stripMarkers(rest, c.noise()))
			break
		}
		out.WriteString(stripMarkers(rest[:start], c.noise()))
		after := rest[start+len(c.spec.start):]
		end := strings.Index(after, c.spec.end)
		if end < 0 {
			// Unterminated block: drop the remainder.
			break
		}
		rest = after[end+len(c.spec.end):]
	}
	return out.String()
}

func (c *markerConverter) noise() []string { return sectionMarkers[c.spec.name] }

// partialMarkerSuffix returns the length of the longest suffix of s that is a
// proper prefix of marker (so it can be completed by the next delta).
func partialMarkerSuffix(s, marker string) int {
	max := len(marker) - 1
	if len(s) < max {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if strings.HasSuffix(s, marker[:k]) {
			return k
		}
	}
	return 0
}

func stripMarkers(s string, markers []string) string {
	for _, m := range markers {
		s = strings.ReplaceAll(s, m, "")
	}
	return s
}

// ---------------------------------------------------------------------------
// payload parsers
// ---------------------------------------------------------------------------

// parseHermesPayload accepts the Hermes JSON shape and its XML-function shape.
func parseHermesPayload(payload string) ([]ToolCall, error) {
	p := strings.TrimSpace(payload)
	if strings.HasPrefix(p, "<function=") {
		return parseFunctionXML(p)
	}
	return parseJSONCalls(p)
}

// parseQwenPayload accepts the JSON shape and a JSON array of calls (Qwen3
// emits one array when it calls several tools at once).
func parseQwenPayload(payload string) ([]ToolCall, error) {
	return parseJSONCalls(strings.TrimSpace(payload))
}

// parseGLMPayload parses `name<arg_key>k</arg_key><arg_value>v</arg_value>…`.
func parseGLMPayload(payload string) ([]ToolCall, error) {
	p := strings.TrimSpace(payload)
	idx := strings.Index(p, "<arg_key>")
	if idx < 0 {
		// Some GLM builds emit the call as JSON; fall back rather than drop it.
		return parseJSONCalls(p)
	}
	call := ToolCall{Name: strings.TrimSpace(p[:idx]), Arguments: map[string]any{}}
	rest := p[idx:]
	for {
		ks := strings.Index(rest, "<arg_key>")
		if ks < 0 {
			break
		}
		afterKey := rest[ks+len("<arg_key>"):]
		ke := strings.Index(afterKey, "</arg_key>")
		if ke < 0 {
			break
		}
		key := strings.TrimSpace(afterKey[:ke])
		afterKey = afterKey[ke+len("</arg_key>"):]
		vs := strings.Index(afterKey, "<arg_value>")
		if vs < 0 {
			break
		}
		afterVal := afterKey[vs+len("<arg_value>"):]
		ve := strings.Index(afterVal, "</arg_value>")
		if ve < 0 {
			break
		}
		call.Arguments[key] = coerceScalar(strings.TrimSpace(afterVal[:ve]))
		rest = afterVal[ve+len("</arg_value>"):]
	}
	if call.Name == "" {
		return nil, fmt.Errorf("glm: no tool name in payload")
	}
	return []ToolCall{call}, nil
}

// parseDeepSeekPayload parses
// `function<|tool▁sep|>NAME\n```json\n{…}\n````.
func parseDeepSeekPayload(payload string) ([]ToolCall, error) {
	p := strings.TrimSpace(payload)
	p = strings.TrimPrefix(p, "<|tool▁call▁begin|>")
	sep := "<|tool▁sep|>"
	if strings.HasPrefix(p, "function") {
		p = strings.TrimSpace(strings.TrimPrefix(p, "function"))
	}
	if i := strings.Index(p, sep); i >= 0 {
		p = p[i+len(sep):]
	}
	name := p
	if i := strings.IndexAny(name, "\n\r"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("deepseek-v3: no tool name in payload")
	}
	args, parseErr, err := firstJSONObject(p)
	if err != nil {
		return nil, err
	}
	return []ToolCall{{Name: name, Arguments: args, ParseError: parseErr}}, nil
}

// parseKimiPayload parses `functions.NAME:IDX<|tool_call_argument_begin|>{…}`.
func parseKimiPayload(payload string) ([]ToolCall, error) {
	p := strings.TrimSpace(payload)
	p = strings.TrimPrefix(p, "functions.")
	head := p
	if i := strings.Index(head, "<|tool_call_argument_begin|>"); i >= 0 {
		head = head[:i]
	} else if i := strings.Index(head, "{"); i >= 0 {
		head = head[:i]
	}
	name := strings.TrimSpace(head)
	if i := strings.LastIndex(name, ":"); i >= 0 {
		// "name:index"
		if _, err := strconv.Atoi(strings.TrimSpace(name[i+1:])); err == nil {
			name = strings.TrimSpace(name[:i])
		}
	}
	if name == "" {
		return nil, fmt.Errorf("kimi-k2: no tool name in payload")
	}
	args, parseErr, err := firstJSONObject(p)
	if err != nil {
		return nil, err
	}
	return []ToolCall{{Name: name, Arguments: args, ParseError: parseErr}}, nil
}

// parseHarmonyPayload parses `functions.NAME<|constrain|>json<|message|>{…}`.
func parseHarmonyPayload(payload string) ([]ToolCall, error) {
	msgIdx := strings.Index(payload, "<|message|>")
	if msgIdx < 0 {
		return nil, fmt.Errorf("harmony: missing <|message|> in payload")
	}
	head := strings.TrimSpace(payload[:msgIdx])
	head = strings.TrimSpace(strings.TrimPrefix(head, "functions."))
	// The constraint marker and its payload ("<|constrain|>json") follow the name.
	if i := strings.Index(head, "<|"); i >= 0 {
		head = strings.TrimSpace(head[:i])
	}
	if head == "" {
		return nil, fmt.Errorf("harmony: no tool name in payload")
	}
	args, parseErr, err := firstJSONObject(payload[msgIdx+len("<|message|>"):])
	if err != nil {
		return nil, err
	}
	return []ToolCall{{Name: head, Arguments: args, ParseError: parseErr}}, nil
}

// parseFunctionXML parses `<function=NAME><parameter=K>V</parameter>…</function>`.
func parseFunctionXML(payload string) ([]ToolCall, error) {
	p := strings.TrimSpace(payload)
	const prefix = "<function="
	if !strings.HasPrefix(p, prefix) {
		return nil, fmt.Errorf("hermes/xml: expected <function=…>")
	}
	rest := p[len(prefix):]
	closeIdx := strings.Index(rest, ">")
	if closeIdx < 0 {
		return nil, fmt.Errorf("hermes/xml: unterminated <function=")
	}
	name := strings.TrimSpace(rest[:closeIdx])
	call := ToolCall{Name: name, Arguments: map[string]any{}}
	body := rest[closeIdx+1:]
	for {
		ps := strings.Index(body, "<parameter=")
		if ps < 0 {
			break
		}
		afterKey := body[ps+len("<parameter="):]
		ke := strings.Index(afterKey, ">")
		if ke < 0 {
			break
		}
		key := strings.TrimSpace(afterKey[:ke])
		afterVal := afterKey[ke+1:]
		ve := strings.Index(afterVal, "</parameter>")
		if ve < 0 {
			break
		}
		call.Arguments[key] = coerceScalar(strings.TrimSpace(afterVal[:ve]))
		body = afterVal[ve+len("</parameter>"):]
	}
	if call.Name == "" {
		return nil, fmt.Errorf("hermes/xml: empty function name")
	}
	return []ToolCall{call}, nil
}

// parseJSONCalls parses the Hermes-style `{"name":…,"arguments":{…}}` object, a
// bare array of those, or the `{"tool_call": {…}}` wrapper some templates use.
func parseJSONCalls(payload string) ([]ToolCall, error) {
	raw := strings.TrimSpace(payload)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("hermes/xml: empty tool call payload")
	}

	var one json.RawMessage
	switch raw[0] {
	case '[':
		var many []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &many); err != nil {
			return nil, fmt.Errorf("hermes/xml: bad tool call array: %w", err)
		}
		out := make([]ToolCall, 0, len(many))
		for _, item := range many {
			call, err := decodeCall(item)
			if err != nil {
				return nil, err
			}
			out = append(out, call)
		}
		return out, nil
	case '{':
		one = json.RawMessage(raw)
	default:
		return nil, fmt.Errorf("hermes/xml: unrecognised tool call payload")
	}
	call, err := decodeCall(one)
	if err != nil {
		return nil, err
	}
	return []ToolCall{call}, nil
}

func decodeCall(raw json.RawMessage) (ToolCall, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return ToolCall{}, fmt.Errorf("hermes/xml: bad tool call object: %w", err)
	}
	if inner, ok := env["tool_call"]; ok {
		return decodeCall(inner)
	}
	if spec, ok := env["function"]; ok {
		// {"function": {"name":…, "arguments":…}}
		var fn struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(spec, &fn); err == nil && fn.Name != "" {
			return ToolCall{Name: fn.Name, Arguments: decodeArguments(fn.Arguments)}, nil
		}
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return ToolCall{}, fmt.Errorf("hermes/xml: bad tool call object: %w", err)
	}
	if call.Name == "" {
		return ToolCall{}, fmt.Errorf("hermes/xml: tool call has no name")
	}
	return ToolCall{Name: call.Name, Arguments: decodeArguments(call.Arguments)}, nil
}

// decodeArguments accepts an object, a JSON-encoded string, or nothing.
func decodeArguments(raw json.RawMessage) map[string]any {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return map[string]any{}
	case strings.HasPrefix(trimmed, "{"):
		var args map[string]any
		if json.Unmarshal(raw, &args) == nil {
			return args
		}
	case strings.HasPrefix(trimmed, `"`):
		var encoded string
		if json.Unmarshal(raw, &encoded) == nil {
			var args map[string]any
			if json.Unmarshal([]byte(encoded), &args) == nil {
				return args
			}
		}
	}
	return map[string]any{"_raw": trimmed}
}

// firstJSONObject returns the first balanced JSON object in s plus a non-empty
// ParseError when the object was truncated (the caller keeps the call rather
// than dropping it).
func firstJSONObject(s string) (map[string]any, string, error) {
	start := strings.Index(s, "{")
	if start < 0 {
		return map[string]any{}, "", nil
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				var args map[string]any
				if err := json.Unmarshal([]byte(s[start:i+1]), &args); err != nil {
					return map[string]any{}, fmt.Sprintf("malformed JSON: %v", err), nil
				}
				return args, "", nil
			}
		}
	}
	return map[string]any{}, fmt.Sprintf("truncated JSON (%d chars)", len(s)-start), nil
}

// coerceScalar renders an XML parameter value with its natural JSON type when it
// is unambiguous, so a numeric argument stays numeric.
func coerceScalar(s string) any {
	if s == "" {
		return ""
	}
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ---------------------------------------------------------------------------
// prompt injection
// ---------------------------------------------------------------------------

func describeXML(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You can call tools. To call one, emit exactly:\n")
	b.WriteString("<tool_call>\n{\"name\": \"<tool name>\", \"arguments\": {<arguments>}}\n</tool_call>\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		fmt.Fprintf(&b, "- %s: %s (parameters: %s)\n", t.Name, t.Description, string(schema))
	}
	return b.String()
}

func describeGLM(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You can call tools. To call one, emit exactly:\n")
	b.WriteString("<tool_call><tool name><arg_key><parameter name></arg_key><arg_value><value></arg_value></tool_call>\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		fmt.Fprintf(&b, "- %s: %s (parameters: %s)\n", t.Name, t.Description, string(schema))
	}
	return b.String()
}

func describeFenced(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You can call tools. To call one, emit exactly:\n")
	b.WriteString("function<|tool▁sep|><tool name>\n```json\n{<arguments>}\n```\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		fmt.Fprintf(&b, "- %s: %s (parameters: %s)\n", t.Name, t.Description, string(schema))
	}
	return b.String()
}

func describeKimi(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You can call tools. To call one, emit exactly:\n")
	b.WriteString("functions.<tool name>:0<|tool_call_argument_begin|>{<arguments>}\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		fmt.Fprintf(&b, "- %s: %s (parameters: %s)\n", t.Name, t.Description, string(schema))
	}
	return b.String()
}

func describeHarmony(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You can call tools. To call one, emit exactly:\n")
	b.WriteString("<|channel|>commentary to=functions.<tool name><|constrain|>json<|message|>{<arguments>}<|call|>\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		schema, _ := json.Marshal(t.Parameters)
		fmt.Fprintf(&b, "- %s: %s (parameters: %s)\n", t.Name, t.Description, string(schema))
	}
	return b.String()
}
