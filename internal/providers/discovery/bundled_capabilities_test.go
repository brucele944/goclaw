package discovery

import "testing"

// TestBundledModelCapabilities_DashScopeCannotStreamWithTools is the seed the
// request path depends on: every DashScope chat model declares
// stream_with_tools=false so the non-stream transport is chosen from the row
// instead of from a provider-name/type check.
func TestBundledModelCapabilities_DashScopeCannotStreamWithTools(t *testing.T) {
	for _, model := range []string{"qwen3.6-plus", "qwen3.5-plus", "qwen3.5-flash", "qwen3.5-turbo", "qwen3-max", "qwen3-plus", "qwen3-turbo"} {
		override, ok := BundledModelCapabilities("dashscope", model)
		if !ok {
			t.Fatalf("dashscope/%s: no declared capabilities, want stream_with_tools=false", model)
		}
		if override.StreamWithTools == nil || *override.StreamWithTools {
			t.Errorf("dashscope/%s: StreamWithTools = %v, want false", model, override.StreamWithTools)
		}
		if override.ToolCalling == nil || !*override.ToolCalling {
			t.Errorf("dashscope/%s: ToolCalling = %v, want true", model, override.ToolCalling)
		}
	}
}

// TestBundledModelCapabilities_NonChatAndUnknownStayUndeclared verifies the
// lookup never invents a declaration: DashScope's image/video entries are not
// chat models, and an unknown provider type or model resolves to "no override"
// so the request path keeps the provider's own Capabilities().
func TestBundledModelCapabilities_NonChatAndUnknownStayUndeclared(t *testing.T) {
	if _, ok := BundledModelCapabilities("dashscope", "wan2.6-image"); ok {
		t.Error("wan2.6-image is not a chat model and must not declare request-path capabilities")
	}
	if _, ok := BundledModelCapabilities("acme-unlisted", "fast-1"); ok {
		t.Error("an unknown provider type must not resolve an override")
	}
	if _, ok := BundledModelCapabilities("dashscope", "qwen-not-a-model"); ok {
		t.Error("an unknown model id must not resolve an override")
	}
}

// TestBundledModelCapabilities_StreamingSeedsDeclareToolsAndStreaming pins the
// seeding of today's behaviour for the entries that already declared
// reasoning/vision: tool calling and streaming-with-tools stay explicitly on.
func TestBundledModelCapabilities_StreamingSeedsDeclareToolsAndStreaming(t *testing.T) {
	override, ok := BundledModelCapabilities("anthropic_native", "claude-sonnet-4-6")
	if !ok {
		t.Fatal("anthropic_native/claude-sonnet-4-6 must declare capabilities")
	}
	if override.ToolCalling == nil || !*override.ToolCalling {
		t.Errorf("ToolCalling = %v, want true", override.ToolCalling)
	}
	if override.StreamWithTools == nil || !*override.StreamWithTools {
		t.Errorf("StreamWithTools = %v, want true", override.StreamWithTools)
	}
	if override.MaxContextWindow != 200_000 {
		t.Errorf("MaxContextWindow = %d, want the seeded 200000", override.MaxContextWindow)
	}
	if override.Vision == nil || !*override.Vision {
		t.Errorf("Vision = %v, want true", override.Vision)
	}
}

// TestBundledCapabilityKeysStayInTheRowDocumentShape guards the contract between
// the shipped snapshot and llm_models.capabilities: the keys the request path
// reads are the same keys modelInfo() writes into the row.
func TestBundledCapabilityKeysStayInTheRowDocumentShape(t *testing.T) {
	info := bundledCatalogs["dashscope"][0].modelInfo()
	if info.Capabilities == nil {
		t.Fatal("dashscope snapshot must declare capabilities")
	}
	if v, ok := info.Capabilities["stream_with_tools"]; !ok || v {
		t.Errorf("row capabilities stream_with_tools = %v (present=%v), want false", v, ok)
	}
	if v, ok := info.Capabilities["tool_calling"]; !ok || !v {
		t.Errorf("row capabilities tool_calling = %v (present=%v), want true", v, ok)
	}
}
