package providers

import "context"

// ConcurrencyGate bounds how many concurrent Chat/ChatStream calls a single
// provider instance may have in flight (llm_providers.settings.max_in_flight).
// An implementation blocks until a slot is available (or ctx is done), runs
// fn synchronously from the caller's point of view, and releases the slot
// when fn returns.
//
// This package intentionally does not depend on internal/scheduler (which
// itself imports internal/agent -> internal/providers; the reverse edge would
// cycle). cmd wires a real implementation backed by scheduler.Lane at
// provider-construction time via WithConcurrencyGate on the concrete
// provider types that also carry a per-provider request timeout
// (internal/providers/request_timeout.go).
type ConcurrencyGate func(ctx context.Context, fn func()) error

// runGated executes fn — a Chat/ChatStream call — through gate when gate is
// non-nil, otherwise runs it directly. Used at each gated provider's
// Chat/ChatStream entry point, mirroring withRequestTimeout's placement.
func runGated(ctx context.Context, gate ConcurrencyGate, fn func() (*ChatResponse, error)) (*ChatResponse, error) {
	if gate == nil {
		return fn()
	}
	var resp *ChatResponse
	var err error
	if gateErr := gate(ctx, func() {
		resp, err = fn()
	}); gateErr != nil {
		return nil, gateErr
	}
	return resp, err
}
