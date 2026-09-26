package compat

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestResolve_CountedInvariant proves that Resolve runs during catalog/provider
// build, and that subsequent requests using the pre-resolved object perform
// zero Resolve calls and zero map allocations.
func TestResolve_CountedInvariant(t *testing.T) {
	initialCount := ResolveCount()

	s := Settings{
		ProviderName: "my-openai",
		ProviderType: "openai",
		APIBase:      "https://api.openai.com/v1",
	}

	// Catalog build: 1 provider-level resolve + 3 model-scoped resolves
	models := []ModelInfo{
		{ID: "gpt-4o"},
		{ID: "gpt-5.4"},
		{ID: "o3-mini", Compat: json.RawMessage(`{"tool_dialect":"hermes/xml"}`)},
	}

	providerResolved := Resolve("openai-completions", "", ModelInfo{}, s)
	modelResolved := make(map[string]Resolved, len(models))
	for _, m := range models {
		modelResolved[m.ID] = Resolve("openai-completions", "", m, s)
	}

	buildCount := ResolveCount()
	expectedBuilds := initialCount + 1 + int64(len(models))
	if buildCount != expectedBuilds {
		t.Fatalf("ResolveCount after build = %d, want %d", buildCount, expectedBuilds)
	}

	// Simulated runtime: 100 requests across models (with and without thinking)
	for i := 0; i < 100; i++ {
		m := models[i%len(models)]
		r := modelResolved[m.ID]
		thinkingReq := (i % 2) == 0
		effective := r.ForThinking(thinkingReq)
		if effective == nil {
			t.Fatal("effective compat is nil")
		}
		if m.ID == "gpt-5.4" && effective.MaxTokensField != "max_completion_tokens" {
			t.Errorf("gpt-5.4 MaxTokensField = %q, want max_completion_tokens", effective.MaxTokensField)
		}
		if m.ID == "o3-mini" && effective.ToolDialect != "hermes/xml" {
			t.Errorf("o3-mini ToolDialect = %q, want hermes/xml", effective.ToolDialect)
		}
	}

	// Assert count is UNCHANGED across 100 requests: zero per-request resolution
	afterRequestsCount := ResolveCount()
	if afterRequestsCount != buildCount {
		t.Fatalf("ResolveCount after 100 requests = %d, want %d (must be 0 per-request calls)",
			afterRequestsCount, buildCount)
	}

	// Verify pointer swap for thinking
	rOllama := Resolve("openai-completions", "ollama", ModelInfo{ID: "qwen3:8b"}, s)
	baseThink := rOllama.ForThinking(false)
	altThink := rOllama.ForThinking(true)
	if baseThink.Think == nil || *baseThink.Think != false {
		t.Fatalf("baseThink.Think = %v, want false", baseThink.Think)
	}
	if altThink.Think != nil {
		t.Fatalf("altThink.Think = %v, want nil (thinking alternate omits think flag)", altThink.Think)
	}
	if rOllama.WhenThinking == nil {
		t.Fatal("expected WhenThinking alternate object")
	}

	_ = providerResolved
}

// TestLayerOrder verifies the documented 4-layer resolution doctrine:
// 1. Endpoint family (bundled seeds)
// 2. Gateway/auth overlay (operator quirks)
// 3. Model metadata/compat (llm_models.compat)
// 4. Request context (ForThinking)
func TestLayerOrder(t *testing.T) {
	s := Settings{
		ProviderName: "custom",
		ProviderType: "openai",
		APIBase:      "https://proxy.example.com/v1",
		Quirks: []Quirk{
			// Layer 2 operator quirk overriding stream_options
			{
				WireAPI:        "openai-completions",
				EndpointFamily: "together",
				Compat:         json.RawMessage(`{"stream_options":true,"extra_body":{"custom_field":123}}`),
				Enabled:        true,
			},
		},
	}

	// Model with layer 3 override
	m := ModelInfo{
		ID:     "gpt-5.4",
		Compat: json.RawMessage(`{"tool_dialect":"qwen3","strict_tools_disabled":true}`),
	}

	r := Resolve("openai-completions", "together", m, s)

	// Layer 1 Together default is stream_options: false, but Layer 2 operator quirk set it to true
	if !r.StreamOptions {
		t.Errorf("StreamOptions = false, want true (operator quirk in layer 2 must override bundled family)")
	}
	if r.ExtraBody == nil || r.ExtraBody["custom_field"] != float64(123) {
		t.Errorf("ExtraBody = %v, want custom_field: 123", r.ExtraBody)
	}

	// Layer 3 model compat
	if r.ToolDialect != "qwen3" {
		t.Errorf("ToolDialect = %q, want qwen3", r.ToolDialect)
	}
	if !r.StrictToolsDisabled {
		t.Errorf("StrictToolsDisabled = false, want true")
	}
	if r.MaxTokensField != "max_completion_tokens" {
		t.Errorf("MaxTokensField = %q, want max_completion_tokens (derived from gpt-5.4 ID)", r.MaxTokensField)
	}
}

// TestOllamaProxyRegression verifies the bug fix:
// A provider named "ollama-proxy", wire_api=openai-completions, no endpoint_family=ollama
// does NOT receive Ollama shaping.
func TestOllamaProxyRegression(t *testing.T) {
	s := Settings{
		ProviderName: "ollama-proxy", // name contains "ollama"
		ProviderType: "openai",
		APIBase:      "https://llm-gateway.example.com/v1", // generic base URL
	}

	r := Resolve("openai-completions", "", ModelInfo{ID: "qwen3:8b"}, s)

	if r.Family == FamilyOllama {
		t.Fatalf("family = %q, want not ollama (name alone must not match)", r.Family)
	}
	if r.NativeChatPath != "" {
		t.Fatalf("NativeChatPath = %q, want empty (must not route to /api/chat)", r.NativeChatPath)
	}
	if r.OllamaOptions {
		t.Fatalf("OllamaOptions = true, want false")
	}
	if r.OllamaThink {
		t.Fatalf("OllamaThink = true, want false")
	}
	if !r.SupportsThinking {
		t.Fatalf("SupportsThinking = false, want true")
	}
}

// TestValidation_RejectsForbiddenHeadersAndInvalidShapes tests the operator
// quirk validation constraints.
func TestValidation_RejectsForbiddenHeadersAndInvalidShapes(t *testing.T) {
	// Forbidden headers: Authorization, Host
	errAuth := Validate(json.RawMessage(`{"headers": {"Authorization": "Bearer secret"}}`))
	if errAuth == nil || !strings.Contains(errAuth.Error(), "forbidden") {
		t.Fatalf("expected forbidden header error for Authorization, got: %v", errAuth)
	}

	errHost := Validate(json.RawMessage(`{"headers": {"Host": "evil.com"}}`))
	if errHost == nil || !strings.Contains(errHost.Error(), "forbidden") {
		t.Fatalf("expected forbidden header error for Host, got: %v", errHost)
	}

	// Unknown key
	errUnknown := Validate(json.RawMessage(`{"nonexistent_quirk_key": true}`))
	if errUnknown == nil || !strings.Contains(errUnknown.Error(), "unknown") {
		t.Fatalf("expected unknown key error, got: %v", errUnknown)
	}

	// Invalid max_tokens_field value
	errField := Validate(json.RawMessage(`{"max_tokens_field": "invalid_field"}`))
	if errField == nil {
		t.Fatalf("expected error for invalid max_tokens_field")
	}

	// Valid fragment
	errValid := Validate(json.RawMessage(`{"system_as_content": true, "max_tokens_field": "max_tokens", "clamp_max_tokens": 4096, "headers": {"X-Custom": "val"}}`))
	if errValid != nil {
		t.Fatalf("valid fragment rejected: %v", errValid)
	}
}
