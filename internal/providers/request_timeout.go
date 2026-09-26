package providers

import (
	"context"
	"time"
)

// Per-provider request deadline (llm_providers.settings.timeout_sec).
//
// The default transport bounds only the wait for response headers (300s, see
// NewDefaultTransport) and deliberately sets no Client.Timeout so long streams
// are not cut off. A provider that declares its own timeout_sec wants a bound on
// the whole call instead, so the deadline is attached to the request context at
// the provider's entry points: it covers connection, retries and streaming, and
// it composes with whatever deadline the caller already had (the tighter one
// wins). Providers that declare nothing keep the previous behaviour exactly.

// withRequestTimeout bounds ctx by d. A non-positive d leaves ctx untouched.
func withRequestTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// WithRequestTimeout sets the per-provider deadline for Chat/ChatStream calls.
// 0 (the default) means no per-provider deadline.
func (p *OpenAIProvider) WithRequestTimeout(d time.Duration) *OpenAIProvider {
	p.requestTimeout = d
	return p
}

// RequestTimeout returns the configured per-provider deadline (0 = none).
func (p *OpenAIProvider) RequestTimeout() time.Duration { return p.requestTimeout }

// WithRequestTimeout sets the per-provider deadline for Chat/ChatStream calls.
func (p *AnthropicProvider) WithRequestTimeout(d time.Duration) *AnthropicProvider {
	p.requestTimeout = d
	return p
}

// RequestTimeout returns the configured per-provider deadline (0 = none).
func (p *AnthropicProvider) RequestTimeout() time.Duration { return p.requestTimeout }

// WithRequestTimeout sets the per-provider deadline for Chat/ChatStream calls.
func (p *CodexProvider) WithRequestTimeout(d time.Duration) *CodexProvider {
	p.requestTimeout = d
	return p
}

// RequestTimeout returns the configured per-provider deadline (0 = none).
func (p *CodexProvider) RequestTimeout() time.Duration { return p.requestTimeout }

// WithRequestTimeout sets the per-provider deadline for Chat/ChatStream calls.
func (p *OllamaProvider) WithRequestTimeout(d time.Duration) *OllamaProvider {
	p.requestTimeout = d
	return p
}

// RequestTimeout returns the configured per-provider deadline (0 = none).
func (p *OllamaProvider) RequestTimeout() time.Duration { return p.requestTimeout }
