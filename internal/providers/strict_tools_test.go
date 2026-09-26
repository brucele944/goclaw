package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestStrictToolsLifecycle_ModelARejectionLeavesModelBStrict verifies that when model A
// encounters an upstream strict rejection error, strict mode is disabled ONLY for model A.
// Model B on the exact same provider and base URL remains strict.
func TestStrictToolsLifecycle_ModelARejectionLeavesModelBStrict(t *testing.T) {
	DefaultStrictTools().Reset()
	defer DefaultStrictTools().Reset()

	var persistedKey *StrictScopeKey
	SetStrictToolsPersistHook(func(key StrictScopeKey) {
		persistedKey = &key
	})

	tools := []ToolDefinition{
		{
			Type: "function",
			Function: &ToolFunctionSchema{
				Name:        "lookup",
				Description: "test tool",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	// Model A initial body has strict: true (because it's openai-native/strict)
	p := NewOpenAIProvider("openai", "key", "https://api.openai.com/v1", "gpt-4o")

	bodyA := p.buildRequestBody("gpt-4o", ChatRequest{Tools: tools}, false)
	toolsPayloadA, _ := bodyA["tools"].([]map[string]any)
	if len(toolsPayloadA) == 0 {
		t.Fatal("expected tools payload")
	}
	fnA := toolsPayloadA[0]["function"].(map[string]any)
	if fnA["strict"] != true {
		t.Fatalf("model A initial strict = %v, want true", fnA["strict"])
	}

	// Simulate model A rejecting strict mode
	keyA := StrictScopeKey{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"}
	DefaultStrictTools().Disable(keyA)

	if !DefaultStrictTools().Disabled(keyA) {
		t.Fatal("expected model A to be marked disabled")
	}
	if persistedKey == nil || *persistedKey != keyA {
		t.Fatalf("persist hook received = %+v, want %+v", persistedKey, keyA)
	}

	// Now model A request body has NO strict: true
	bodyAAfter := p.buildRequestBody("gpt-4o", ChatRequest{Tools: tools}, false)
	toolsPayloadAAfter, _ := bodyAAfter["tools"].([]map[string]any)
	fnAAfter := toolsPayloadAAfter[0]["function"].(map[string]any)
	if fnAAfter["strict"] == true {
		t.Fatalf("model A after rejection strict = true, want omitted/false")
	}

	// Model B on the same provider/base URL MUST STILL BE STRICT
	bodyB := p.buildRequestBody("gpt-4o-mini", ChatRequest{Tools: tools}, false)
	toolsPayloadB, _ := bodyB["tools"].([]map[string]any)
	fnB := toolsPayloadB[0]["function"].(map[string]any)
	if fnB["strict"] != true {
		t.Fatalf("model B strict = %v, want true (model B must stay strict when model A rejected)", fnB["strict"])
	}

	keyB := StrictScopeKey{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini"}
	if DefaultStrictTools().Disabled(keyB) {
		t.Fatal("model B should NOT be marked disabled")
	}
}

// TestStrictTools_AutoRetryOnRejection asserts that when Chat encounters a 400
// error indicating strict rejection, it auto-retries once without strict mode.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestStrictTools_AutoRetryOnRejection(t *testing.T) {
	DefaultStrictTools().Reset()
	defer DefaultStrictTools().Reset()

	var attempts atomic.Int64
	var lastReceivedBody string

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			att := attempts.Add(1)
			bodyBytes, _ := io.ReadAll(req.Body)
			lastReceivedBody = string(bodyBytes)

			if att == 1 {
				// First attempt fails with 400 strict rejection
				return &http.Response{
					StatusCode: 400,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"strict mode is not supported for this model schema"}}`)),
					Header:     make(http.Header),
				}, nil
			}
			// Second attempt succeeds
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ok","choices":[{"message":{"role":"assistant","content":"success"}}]}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	p := NewOpenAIProvider("openai", "key", "https://api.openai.com/v1", "gpt-custom").WithHTTPClient(client)

	tools := []ToolDefinition{
		{
			Type: "function",
			Function: &ToolFunctionSchema{
				Name:        "lookup",
				Description: "test tool",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}

	resp, err := p.Chat(context.Background(), ChatRequest{Model: "gpt-custom", Tools: tools})
	if err != nil {
		t.Fatalf("Chat failed to retry on strict rejection: %v", err)
	}
	if resp.Content != "success" {
		t.Fatalf("resp.Content = %q, want success", resp.Content)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if strings.Contains(lastReceivedBody, `"strict":true`) {
		t.Fatalf("second attempt still had strict:true in body: %s", lastReceivedBody)
	}
}
