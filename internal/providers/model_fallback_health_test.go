package providers

import (
	"context"
	"testing"
	"time"
)

// TestFallbackHitsSecondaryAndPersistsCooldown is phase 5's provider-level chain
// acceptance test: a primary that answers 429 with a declared secondary moves to
// the secondary, and the failure lands in the durable store (so the cooldown
// survives the process).
func TestFallbackHitsSecondaryAndPersistsCooldown(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()

	primary := &testFallbackProvider{
		name:  "primary",
		model: "primary-model",
		err:   &HTTPError{Status: 429, Body: "rate limited"},
	}
	secondary := &testFallbackProvider{name: "secondary", model: "secondary-model"}

	provider := NewModelFallbackProvider(
		FallbackCandidate{ProviderName: "primary", Model: "primary-model", Provider: primary},
		[]FallbackCandidate{{ProviderName: "secondary", Model: "secondary-model", Provider: secondary}},
		0, true, store,
	)
	provider.tracker.nowFn = func() time.Time { return now }

	resp, err := provider.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v, want the secondary to serve the request", err)
	}
	if resp.Content != "secondary-model" {
		t.Errorf("response came from %q, want the secondary", resp.Content)
	}
	if primary.calls != 1 || secondary.calls != 1 {
		t.Errorf("calls primary=%d secondary=%d, want 1/1", primary.calls, secondary.calls)
	}

	recorded, ok := store.lastRecorded()
	if !ok {
		t.Fatal("the primary failure was not persisted: a restart would retry it immediately")
	}
	if recorded.provider != "primary" {
		t.Errorf("persisted failure for %q, want the primary", recorded.provider)
	}
	if recorded.errorClass != string(FailoverRateLimit) {
		t.Errorf("persisted error class = %q, want %q", recorded.errorClass, string(FailoverRateLimit))
	}
	if want := now.Add(30 * time.Second); !recorded.cooldownUntil.Equal(want) {
		t.Errorf("persisted cooldown_until = %v, want %v", recorded.cooldownUntil, want)
	}
}

// TestFallbackCooldownSurvivesRestart: the whole point of the persistent store.
// A second wrapper with a brand new tracker (fresh process, empty in-memory map)
// over the same durable state must skip the cooling primary and serve from the
// secondary — even though the primary is healthy again by then.
func TestFallbackCooldownSurvivesRestart(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()

	primary := &testFallbackProvider{
		name:  "primary",
		model: "primary-model",
		err:   &HTTPError{Status: 429, Body: "rate limited"},
	}
	secondary := &testFallbackProvider{name: "secondary", model: "secondary-model"}

	// Process 1: two requests, so the second one takes the allowed probe path and
	// leaves a persisted probe stamp behind (runOrdered: availability, then probe).
	first := NewModelFallbackProvider(
		FallbackCandidate{ProviderName: "primary", Model: "primary-model", Provider: primary},
		[]FallbackCandidate{{ProviderName: "secondary", Model: "secondary-model", Provider: secondary}},
		0, true, store,
	)
	first.tracker.nowFn = func() time.Time { return now }
	for range 2 {
		if _, err := first.Chat(context.Background(), ChatRequest{}); err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
	}
	if primary.calls != 2 {
		t.Fatalf("primary calls in process 1 = %d, want 2 (initial failure + probe)", primary.calls)
	}
	if store.probeCount() == 0 {
		t.Fatal("expected the probe stamp to be persisted")
	}

	// Process 2: new tracker, same durable state. The primary is healthy now, so
	// any call to it is a cooldown leak.
	healthyPrimary := &testFallbackProvider{name: "primary", model: "primary-model"}
	freshSecondary := &testFallbackProvider{name: "secondary", model: "secondary-model"}
	second := NewModelFallbackProvider(
		FallbackCandidate{ProviderName: "primary", Model: "primary-model", Provider: healthyPrimary},
		[]FallbackCandidate{{ProviderName: "secondary", Model: "secondary-model", Provider: freshSecondary}},
		0, true, store,
	)
	second.tracker.nowFn = func() time.Time { return now }

	resp, err := second.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() after restart error = %v", err)
	}
	if healthyPrimary.calls != 0 {
		t.Errorf("primary called %d times after restart: the durable cooldown was forgotten", healthyPrimary.calls)
	}
	if freshSecondary.calls != 1 {
		t.Errorf("secondary calls after restart = %d, want 1", freshSecondary.calls)
	}
	if resp.Content != "secondary-model" {
		t.Errorf("response came from %q, want the secondary", resp.Content)
	}
}

// TestFallbackCooldownCanBeCleared: a success clears the durable state, so the
// primary comes back into rotation without waiting for the deadline.
func TestFallbackCooldownCanBeCleared(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()

	primary := &testFallbackProvider{
		name:  "primary",
		model: "primary-model",
		err:   &HTTPError{Status: 429, Body: "rate limited"},
	}
	secondary := &testFallbackProvider{name: "secondary", model: "secondary-model"}

	provider := NewModelFallbackProvider(
		FallbackCandidate{ProviderName: "primary", Model: "primary-model", Provider: primary},
		[]FallbackCandidate{{ProviderName: "secondary", Model: "secondary-model", Provider: secondary}},
		0, true, store,
	)
	provider.tracker.nowFn = func() time.Time { return now }

	if _, err := provider.Chat(context.Background(), ChatRequest{}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// The secondary served the request, so it records a success — which clears the
	// secondary's durable state (there is none) and must not clear the primary's.
	if _, ok := store.lastRecorded(); !ok {
		t.Fatal("primary failure should still be persisted after the secondary succeeded")
	}

	// An explicit success for the primary clears its cooldown.
	provider.tracker.RecordSuccess(context.Background(), "primary", "primary-model")
	if _, ok := store.lastRecorded(); !ok {
		t.Fatal("expected the failure history to remain after a success")
	}
	if state, _, err := store.CooldownState(context.Background(), "primary"); err != nil {
		t.Fatalf("CooldownState() error = %v", err)
	} else if !state.CooldownUntil.IsZero() {
		t.Errorf("durable cooldown = %v, want cleared", state.CooldownUntil)
	}
}

// TestFallbackCooldownDisabledDoesNotPersist: cooldown_enabled=false keeps the
// pre-existing opt-out — retry immediately, write nothing durable.
func TestFallbackCooldownDisabledDoesNotPersist(t *testing.T) {
	store := newFakeCooldownStore()
	primary := &testFallbackProvider{
		name:  "primary",
		model: "primary-model",
		err:   &HTTPError{Status: 429, Body: "rate limited"},
	}
	secondary := &testFallbackProvider{name: "secondary", model: "secondary-model"}

	provider := NewModelFallbackProvider(
		FallbackCandidate{ProviderName: "primary", Model: "primary-model", Provider: primary},
		[]FallbackCandidate{{ProviderName: "secondary", Model: "secondary-model", Provider: secondary}},
		0, false, store,
	)
	if provider.tracker != nil {
		t.Fatal("cooldown_enabled=false must not install a tracker")
	}

	if _, err := provider.Chat(context.Background(), ChatRequest{}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, ok := store.lastRecorded(); ok {
		t.Error("nothing may be persisted when cooldown is disabled")
	}
}
