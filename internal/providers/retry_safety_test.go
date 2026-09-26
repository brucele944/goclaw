package providers

import (
	"context"
	"sync/atomic"
	"testing"
)

// fakeStreamingProvider is a test double that simulates partial stream emission
// followed by an error on the first attempt, and success on subsequent attempts.
type fakeStreamingProvider struct {
	name          string
	defaultModel  string
	attempts      atomic.Int64
	chunksOnFirst []StreamChunk
	errOnFirst    error
	respOnSecond  *ChatResponse
}

func (f *fakeStreamingProvider) Name() string         { return f.name }
func (f *fakeStreamingProvider) DefaultModel() string { return f.defaultModel }
func (f *fakeStreamingProvider) SupportsThinking() bool { return true }
func (f *fakeStreamingProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Streaming: true}
}
func (f *fakeStreamingProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	attempt := f.attempts.Add(1)
	if attempt == 1 && f.errOnFirst != nil {
		return nil, f.errOnFirst
	}
	return f.respOnSecond, nil
}
func (f *fakeStreamingProvider) ChatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk)) (*ChatResponse, error) {
	attempt := f.attempts.Add(1)
	if attempt == 1 {
		for _, c := range f.chunksOnFirst {
			if onChunk != nil {
				onChunk(c)
			}
		}
		if f.errOnFirst != nil {
			return nil, f.errOnFirst
		}
	}
	if f.respOnSecond != nil && onChunk != nil {
		onChunk(StreamChunk{Content: f.respOnSecond.Content})
	}
	return f.respOnSecond, nil
}

// TestFallback_ThinkingOnlyPartialIsRetried asserts that when a stream emits ONLY
// thinking tokens before failing with a 500 error, failover to the fallback candidate
// succeeds (thinking-only partials are safe to discard).
func TestFallback_ThinkingOnlyPartialIsRetried(t *testing.T) {
	primary := &fakeStreamingProvider{
		name:         "primary",
		defaultModel: "model-a",
		chunksOnFirst: []StreamChunk{
			{Thinking: "let me ponder this..."},
			{Thinking: "still thinking..."},
		},
		errOnFirst: &HTTPError{Status: 500, Body: "internal server error"},
	}
	fallback := &fakeStreamingProvider{
		name:         "fallback",
		defaultModel: "model-b",
		respOnSecond: &ChatResponse{Content: "fallback-ok", FinishReason: "stop"},
	}

	fb := NewModelFallbackProvider(
		FallbackCandidate{Provider: primary, ProviderName: "primary", Model: "model-a"},
		[]FallbackCandidate{
			{Provider: fallback, ProviderName: "fallback", Model: "model-b"},
		},
		2,
		false,
		nil,
	)

	var receivedChunks []StreamChunk
	resp, err := fb.ChatStream(context.Background(), ChatRequest{Model: "model-a"}, func(c StreamChunk) {
		receivedChunks = append(receivedChunks, c)
	})
	if err != nil {
		t.Fatalf("ChatStream with thinking-only partial failed to fallback: %v", err)
	}
	if resp.Content != "fallback-ok" {
		t.Fatalf("response content = %q, want fallback-ok", resp.Content)
	}
	if fallback.attempts.Load() != 1 {
		t.Fatalf("fallback provider attempts = %d, want 1 (should have failed over)", fallback.attempts.Load())
	}
}

// TestFallback_EmittedContentNeverRetried asserts that when a stream emits visible
// text before failing, failover is ABORTED to prevent duplicated output.
func TestFallback_EmittedContentNeverRetried(t *testing.T) {
	primary := &fakeStreamingProvider{
		name:         "primary",
		defaultModel: "model-a",
		chunksOnFirst: []StreamChunk{
			{Content: "Hello user, I am starting to"},
		},
		errOnFirst: &HTTPError{Status: 500, Body: "connection dropped midway"},
	}
	fallback := &fakeStreamingProvider{
		name:         "fallback",
		defaultModel: "model-b",
		respOnSecond: &ChatResponse{Content: "fallback-ok"},
	}

	fb := NewModelFallbackProvider(
		FallbackCandidate{Provider: primary, ProviderName: "primary", Model: "model-a"},
		[]FallbackCandidate{
			{Provider: fallback, ProviderName: "fallback", Model: "model-b"},
		},
		2,
		false,
		nil,
	)

	resp, err := fb.ChatStream(context.Background(), ChatRequest{Model: "model-a"}, func(c StreamChunk) {})
	if err == nil {
		t.Fatalf("ChatStream expected error after emitted content, got resp: %+v", resp)
	}
	if fallback.attempts.Load() != 0 {
		t.Fatalf("fallback provider attempts = %d, want 0 (must not failover after visible content)", fallback.attempts.Load())
	}
}

// TestFallback_WhitespaceOnlyPartialIsRetried asserts that whitespace-only
// stream chunks are considered safe to discard and retry.
func TestFallback_WhitespaceOnlyPartialIsRetried(t *testing.T) {
	primary := &fakeStreamingProvider{
		name:         "primary",
		defaultModel: "model-a",
		chunksOnFirst: []StreamChunk{
			{Content: "   \n\t  "},
		},
		errOnFirst: &HTTPError{Status: 500, Body: "server error"},
	}
	fallback := &fakeStreamingProvider{
		name:         "fallback",
		defaultModel: "model-b",
		respOnSecond: &ChatResponse{Content: "fallback-ok"},
	}

	fb := NewModelFallbackProvider(
		FallbackCandidate{Provider: primary, ProviderName: "primary", Model: "model-a"},
		[]FallbackCandidate{
			{Provider: fallback, ProviderName: "fallback", Model: "model-b"},
		},
		2,
		false,
		nil,
	)

	resp, err := fb.ChatStream(context.Background(), ChatRequest{Model: "model-a"}, func(c StreamChunk) {})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if resp.Content != "fallback-ok" {
		t.Fatalf("content = %q, want fallback-ok", resp.Content)
	}
	if fallback.attempts.Load() != 1 {
		t.Fatalf("fallback attempts = %d, want 1", fallback.attempts.Load())
	}
}

// TestFallback_ImagePartialNeverRetried asserts that emitted image frames
// block fallback.
func TestFallback_ImagePartialNeverRetried(t *testing.T) {
	primary := &fakeStreamingProvider{
		name:         "primary",
		defaultModel: "model-a",
		chunksOnFirst: []StreamChunk{
			{Images: []ImageContent{{MimeType: "image/png", Data: "abc"}}},
		},
		errOnFirst: &HTTPError{Status: 500, Body: "server error"},
	}
	fallback := &fakeStreamingProvider{
		name:         "fallback",
		defaultModel: "model-b",
		respOnSecond: &ChatResponse{Content: "fallback-ok"},
	}

	fb := NewModelFallbackProvider(
		FallbackCandidate{Provider: primary, ProviderName: "primary", Model: "model-a"},
		[]FallbackCandidate{
			{Provider: fallback, ProviderName: "fallback", Model: "model-b"},
		},
		2,
		false,
		nil,
	)

	_, err := fb.ChatStream(context.Background(), ChatRequest{Model: "model-a"}, func(c StreamChunk) {})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if fallback.attempts.Load() != 0 {
		t.Fatalf("fallback attempts = %d, want 0", fallback.attempts.Load())
	}
}
