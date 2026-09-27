package providers

import (
	"encoding/json"
	"testing"
)

// buildAnthropicBody is a thin helper over the real request builder so the test
// exercises the same wiring the provider uses.
func buildAnthropicBody(t *testing.T, req ChatRequest) map[string]any {
	t.Helper()
	body := (&AnthropicProvider{}).buildRequestBody("claude-sonnet-4-5", req, false)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return out
}

func anthropicRequestWithTools(choice any) ChatRequest {
	req := ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}}
	req.Tools = []ToolDefinition{{Type: "function", Function: &ToolFunctionSchema{Name: "get_weather", Parameters: map[string]any{"type": "object"}}}}
	if choice != nil {
		req.Options = map[string]any{OptToolChoice: choice}
	}
	return req
}

// TestAnthropicToolChoiceTranslation pins the mapping of the accepted
// OpenAI-shaped tool_choice contract onto Anthropic's schema, including the one
// value with no Anthropic member ("none" withholds the tool list instead of being
// silently dropped) and the named-function form the HTTP surface forwards.
func TestAnthropicToolChoiceTranslation(t *testing.T) {
	cases := []struct {
		name       string
		choice     any
		wantChoice any
		wantTools  bool
	}{
		{name: "absent keeps provider default", choice: nil, wantChoice: nil, wantTools: true},
		{name: "auto", choice: "auto", wantChoice: map[string]any{"type": "auto"}, wantTools: true},
		{name: "required becomes any", choice: "required", wantChoice: map[string]any{"type": "any"}, wantTools: true},
		{name: "none withholds tools", choice: "none", wantChoice: nil, wantTools: false},
		{name: "named function becomes tool", choice: map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}},
			wantChoice: map[string]any{"type": "tool", "name": "get_weather"}, wantTools: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildAnthropicBody(t, anthropicRequestWithTools(tc.choice))
			tools, hasTools := body["tools"]
			if hasTools != tc.wantTools {
				t.Fatalf("tools present = %v, want %v", hasTools, tc.wantTools)
			}
			if hasTools {
				list, ok := tools.([]any)
				if !ok || len(list) != 1 {
					t.Fatalf("tools = %#v, want exactly the one declared tool", tools)
				}
			}
			got, hasChoice := body["tool_choice"]
			if tc.wantChoice == nil {
				if hasChoice {
					t.Fatalf("tool_choice = %#v, want it omitted", got)
				}
				return
			}
			gotMap, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("tool_choice = %#v, want an object", got)
			}
			want := tc.wantChoice.(map[string]any)
			if len(gotMap) != len(want) {
				t.Fatalf("tool_choice = %#v, want %#v", gotMap, want)
			}
			for k, v := range want {
				if gotMap[k] != v {
					t.Fatalf("tool_choice[%q] = %#v, want %#v", k, gotMap[k], v)
				}
			}
		})
	}
}
