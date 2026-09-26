package providers

// Per-provider max_in_flight gate (llm_providers.settings.max_in_flight).
//
// Wired the same way as the per-provider request timeout
// (internal/providers/request_timeout.go): cmd constructs a
// scheduler.Lane-backed ConcurrencyGate at provider-registration time and
// attaches it via WithConcurrencyGate. A provider with no configured
// max_in_flight keeps gate nil, and Chat/ChatStream run unbounded exactly as
// before this feature existed.

// WithConcurrencyGate sets the per-provider max_in_flight gate for
// Chat/ChatStream calls. nil (the default) means unbounded concurrency.
func (p *OpenAIProvider) WithConcurrencyGate(gate ConcurrencyGate) *OpenAIProvider {
	p.concurrencyGate = gate
	return p
}

// ConcurrencyGate returns the configured per-provider gate (nil = unbounded).
func (p *OpenAIProvider) ConcurrencyGate() ConcurrencyGate { return p.concurrencyGate }

// WithConcurrencyGate sets the per-provider max_in_flight gate for
// Chat/ChatStream calls. nil (the default) means unbounded concurrency.
func (p *AnthropicProvider) WithConcurrencyGate(gate ConcurrencyGate) *AnthropicProvider {
	p.concurrencyGate = gate
	return p
}

// ConcurrencyGate returns the configured per-provider gate (nil = unbounded).
func (p *AnthropicProvider) ConcurrencyGate() ConcurrencyGate { return p.concurrencyGate }

// WithConcurrencyGate sets the per-provider max_in_flight gate for
// Chat/ChatStream calls. nil (the default) means unbounded concurrency.
func (p *CodexProvider) WithConcurrencyGate(gate ConcurrencyGate) *CodexProvider {
	p.concurrencyGate = gate
	return p
}

// ConcurrencyGate returns the configured per-provider gate (nil = unbounded).
func (p *CodexProvider) ConcurrencyGate() ConcurrencyGate { return p.concurrencyGate }

// WithConcurrencyGate sets the per-provider max_in_flight gate for
// Chat/ChatStream calls. nil (the default) means unbounded concurrency.
func (p *OllamaProvider) WithConcurrencyGate(gate ConcurrencyGate) *OllamaProvider {
	p.concurrencyGate = gate
	return p
}

// ConcurrencyGate returns the configured per-provider gate (nil = unbounded).
func (p *OllamaProvider) ConcurrencyGate() ConcurrencyGate { return p.concurrencyGate }
