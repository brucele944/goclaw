package dialect

import (
	"reflect"
	"strings"
	"testing"
)

// cannedStreams are the fixture cases: a real-looking model output per family,
// the calls that must come out, and the visible text that must remain.
func cannedStreams() []struct {
	dialect     string
	name        string
	chunks      []string
	wantCalls   []ToolCall
	wantContent string
} {
	hanoi := map[string]any{"city": "Hanoi"}
	return []struct {
		dialect     string
		name        string
		chunks      []string
		wantCalls   []ToolCall
		wantContent string
	}{
		{
			dialect: "qwen3",
			name:    "json object across chunks",
			chunks: []string{
				"Checking. ",
				"<tool_call>\n{\"name\": \"get_weather\", \"argu",
				"ments\": {\"city\": \"Hanoi\"}}\n</tool_call>",
			},
			wantCalls:   []ToolCall{{Name: "get_weather", Arguments: hanoi}},
			wantContent: "Checking. ",
		},
		{
			dialect: "qwen3",
			name:    "json array of two calls",
			chunks: []string{
				"<tool_call>[{\"name\":\"a\",\"arguments\":{\"x\":1}},{\"name\":\"b\",\"arguments\":{}}]</tool_call>",
			},
			wantCalls: []ToolCall{
				{Name: "a", Arguments: map[string]any{"x": float64(1)}},
				{Name: "b", Arguments: map[string]any{}},
			},
			wantContent: "",
		},
		{
			dialect: "hermes/xml",
			name:    "json object",
			chunks: []string{
				"<tool_call>{\"name\": \"lookup\", \"arguments\": {\"id\": \"42\"}}</tool_call>",
			},
			wantCalls:   []ToolCall{{Name: "lookup", Arguments: map[string]any{"id": "42"}}},
			wantContent: "",
		},
		{
			dialect: "hermes/xml",
			name:    "xml function shape",
			chunks: []string{
				"ok ",
				"<tool_call><function=get_weather><parameter=city>Hanoi</parameter>",
				"<parameter=days>3</parameter></function></tool_call>",
			},
			wantCalls: []ToolCall{{
				Name:      "get_weather",
				Arguments: map[string]any{"city": "Hanoi", "days": int64(3)},
			}},
			wantContent: "ok ",
		},
		{
			dialect: "glm",
			name:    "arg_key arg_value",
			chunks: []string{
				"<tool_call>get_weather<arg_key>city</arg_key><arg_value>Hanoi</arg_value></tool_call>",
			},
			wantCalls:   []ToolCall{{Name: "get_weather", Arguments: hanoi}},
			wantContent: "",
		},
		{
			dialect: "deepseek-v3",
			name:    "fenced json section",
			chunks: []string{
				"<|tool▁calls▁begin|><|tool▁call▁begin|>function<|tool▁sep|>get_weather\n```json\n{\"city\":\"Hanoi\"}\n```",
				"<|tool▁call▁end|><|tool▁calls▁end|>",
			},
			wantCalls:   []ToolCall{{Name: "get_weather", Arguments: hanoi}},
			wantContent: "",
		},
		{
			dialect: "kimi-k2",
			name:    "section with function:index",
			chunks: []string{
				"<|tool_calls_section_begin|><|tool_call_begin|>functions.get_weather:0",
				"<|tool_call_argument_begin|>{\"city\":\"Hanoi\"}<|tool_call_end|><|tool_calls_section_end|>",
			},
			wantCalls:   []ToolCall{{Name: "get_weather", Arguments: hanoi}},
			wantContent: "",
		},
		{
			dialect: "harmony",
			name:    "commentary channel",
			chunks: []string{
				"<|channel|>commentary to=functions.get_weather<|constrain|>json<|message|>{\"city\":\"Hanoi\"}<|call|>",
			},
			wantCalls:   []ToolCall{{Name: "get_weather", Arguments: hanoi}},
			wantContent: "",
		},
	}
}

// TestScanStreamParsesCannedFixtures pins that every converter turns its
// family's real stream shape into a structured call, and that the surrounding
// text survives.
func TestScanStreamParsesCannedFixtures(t *testing.T) {
	for _, tc := range cannedStreams() {
		tc := tc
		t.Run(tc.dialect+"/"+tc.name, func(t *testing.T) {
			conv, ok := For(tc.dialect)
			if !ok {
				t.Fatalf("no converter registered for %q", tc.dialect)
			}
			var (
				st      State
				content strings.Builder
				calls   []ToolCall
			)
			for _, chunk := range tc.chunks {
				out, got, err := conv.ScanStream(chunk, &st)
				if err != nil {
					t.Fatalf("ScanStream(%q): %v", chunk, err)
				}
				content.WriteString(out)
				calls = append(calls, got...)
			}
			if got := content.String(); got != tc.wantContent {
				t.Errorf("visible content = %q, want %q", got, tc.wantContent)
			}
			if len(calls) != len(tc.wantCalls) {
				t.Fatalf("calls = %+v, want %+v", calls, tc.wantCalls)
			}
			for i := range calls {
				if calls[i].Name != tc.wantCalls[i].Name {
					t.Errorf("call[%d].Name = %q, want %q", i, calls[i].Name, tc.wantCalls[i].Name)
				}
				if !reflect.DeepEqual(calls[i].Arguments, tc.wantCalls[i].Arguments) {
					t.Errorf("call[%d].Arguments = %#v, want %#v", i, calls[i].Arguments, tc.wantCalls[i].Arguments)
				}
				if calls[i].ID == "" {
					t.Errorf("call[%d] has no generated id", i)
				}
			}
		})
	}
}

// TestHealLeakedMarkup covers the leak repair: a complete block, a dangling
// (unterminated) block at the end of the message, and surrounding prose.
func TestHealLeakedMarkup(t *testing.T) {
	cases := []struct {
		dialect string
		in      string
		want    string
	}{
		{
			dialect: "qwen3",
			in:      "Before <tool_call>{\"name\":\"a\",\"arguments\":{}}</tool_call> after",
			want:    "Before  after",
		},
		{
			dialect: "qwen3",
			in:      "Answer so far<tool_call>{\"name\":\"a\",\"argu",
			want:    "Answer so far",
		},
		{
			dialect: "deepseek-v3",
			in:      "text<|tool▁calls▁begin|><|tool▁call▁begin|>function<|tool▁sep|>a\n```json\n{}</tool_call>",
			want:    "text",
		},
		{
			dialect: "kimi-k2",
			in:      "hi<|tool_calls_section_begin|><|tool_call_begin|>functions.a:0<|tool_call_argument_begin|>{}<|tool_call_end|>",
			want:    "hi",
		},
		{
			dialect: "hermes/xml",
			in:      "plain answer",
			want:    "plain answer",
		},
	}
	for _, tc := range cases {
		conv, _ := For(tc.dialect)
		if got := conv.Heal(tc.in); got != tc.want {
			t.Errorf("%s Heal(%q) = %q, want %q", tc.dialect, tc.in, got, tc.want)
		}
	}
}

// TestSelectEnvOverrideForceHermes is the acceptance case: GOCLAW_TOOL_DIALECT
// forces the converter for a model/wire that declares none.
func TestSelectEnvOverrideForceHermes(t *testing.T) {
	t.Setenv(EnvOverrideVar, "hermes")

	conv := Select("openai-completions", "")
	if conv == nil {
		t.Fatal("Select returned no converter with GOCLAW_TOOL_DIALECT=hermes")
	}
	if conv.Name() != "hermes/xml" {
		t.Fatalf("converter = %q, want hermes/xml", conv.Name())
	}

	var st State
	_, calls, err := conv.ScanStream("<tool_call>{\"name\":\"lookup\",\"arguments\":{\"id\":\"1\"}}</tool_call>", &st)
	if err != nil || len(calls) != 1 || calls[0].Name != "lookup" {
		t.Fatalf("env-forced hermes parse = %+v (err %v), want one lookup call", calls, err)
	}
	if got := conv.Heal("leaked <tool_call>{\"name\":\"a\"}"); got != "leaked " {
		t.Fatalf("env-forced hermes Heal = %q, want %q", got, "leaked ")
	}
}

func TestSelectDeclaredDialectAndDefaults(t *testing.T) {
	t.Setenv(EnvOverrideVar, "")
	if c := Select("openai-completions", "xml"); c == nil || c.Name() != "hermes/xml" {
		t.Fatalf("declared xml → %v, want hermes/xml", c)
	}
	if c := Select("openai-completions", "kimi-k2"); c == nil || c.Name() != "kimi-k2" {
		t.Fatalf("declared kimi-k2 → %v, want kimi-k2", c)
	}
	if c := Select("openai-completions", ""); c != nil {
		t.Fatalf("no declaration → %v, want nil (native tool calls)", c)
	}
	if c := Select("openai-completions", "native"); c != nil {
		t.Fatalf("explicit native → %v, want nil", c)
	}
}

func TestInjectPromptDescribesFamilyProtocol(t *testing.T) {
	tools := []Tool{{Name: "get_weather", Description: "weather", Parameters: map[string]any{"type": "object"}}}
	requiredMarkers := map[string]string{
		"hermes/xml":  "<tool_call>",
		"qwen3":       "<tool_call>",
		"glm":         "<tool_call>",
		"deepseek-v3": "<|tool▁sep|>",
		"kimi-k2":     "<|tool_call_argument_begin|>",
		"harmony":     "<|message|>",
	}
	for _, name := range Names() {
		conv, _ := For(name)
		got := conv.InjectPrompt("SYS", tools)
		if !strings.HasPrefix(got, "SYS") {
			t.Errorf("%s InjectPrompt dropped the system prompt: %q", name, got)
		}
		if !strings.Contains(got, "get_weather") {
			t.Errorf("%s InjectPrompt does not describe the tools: %q", name, got)
		}
		if marker, ok := requiredMarkers[name]; ok && !strings.Contains(got, marker) {
			t.Errorf("%s InjectPrompt does not show the call format (missing %q): %q", name, marker, got)
		}
	}
	if got := describeXML(nil); got == "" {
		t.Error("describeXML(nil) must still describe the protocol")
	}
}

// TestNamesListsSixFamilies guards the registry surface (the six reachable
// families, no more and no fewer without a deliberate edit).
func TestNamesListsSixFamilies(t *testing.T) {
	want := []string{"deepseek-v3", "glm", "harmony", "hermes/xml", "kimi-k2", "qwen3"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

// TestTruncatedArgumentsKeepTheCall pins the healing path for a truncated JSON
// body: the call survives with a ParseError rather than being dropped.
func TestTruncatedArgumentsKeepTheCall(t *testing.T) {
	conv, _ := For("deepseek-v3")
	var st State
	_, calls, err := conv.ScanStream(
		"<|tool▁call▁begin|>function<|tool▁sep|>a\n```json\n{\"x\": 1<|tool▁call▁end|>", &st)
	if err != nil {
		t.Fatalf("ScanStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want one call kept with a ParseError", calls)
	}
	if calls[0].Name != "a" || calls[0].ParseError == "" {
		t.Fatalf("call = %+v, want name=a with a non-empty ParseError", calls[0])
	}
}
