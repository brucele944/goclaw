package providers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The cooldown tests share one provider/model pair, so a call site reads as
// tracker.IsAvailable(ctx, cdProvider, cdModel).
const (
	cdProvider = "openai"
	cdModel    = "gpt-4"
)

func cdCtx() context.Context { return context.Background() }

func TestCooldownKey(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		expected string
	}{
		{"openai", "gpt-4", "openai:gpt-4"},
		{"anthropic", "claude-3-opus", "anthropic:claude-3-opus"},
		{"groq", "mixtral-8x7b", "groq:mixtral-8x7b"},
		{"openai", "gpt-4-turbo", "openai:gpt-4-turbo"},
	}

	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			result := CooldownKey(tt.provider, tt.model)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestNewCooldownTracker(t *testing.T) {
	tests := []struct {
		name      string
		maxKeys   int
		expectMax int
	}{
		{"default max keys", 0, defaultMaxKeys},
		{"negative max keys", -1, defaultMaxKeys},
		{"custom max keys", 256, 256},
		{"large max keys", 10000, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewCooldownTracker(tt.maxKeys, nil)
			if tracker.maxKeys != tt.expectMax {
				t.Errorf("maxKeys: got %d, want %d", tracker.maxKeys, tt.expectMax)
			}
			if tracker.nowFn == nil {
				t.Error("nowFn not initialized")
			}
			if len(tracker.entries) != 0 {
				t.Error("entries should be empty on init")
			}
			if tracker.store != nil {
				t.Error("store should be nil when none is passed (in-memory only)")
			}
		})
	}
}

func TestRecordFailureAndIsAvailable(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Initially available
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("key should be available initially")
	}

	// Record failure
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverRateLimit)

	// Should not be available immediately after failure
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("key should not be available after failure")
	}

	// Advance time past cooldown duration (rate_limit = 30s)
	now = now.Add(31 * time.Second)
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("key should be available after cooldown expires")
	}
}

func TestRecordFailureCooldownDurations(t *testing.T) {
	tests := []struct {
		reason           FailoverReason
		expectedDuration time.Duration
	}{
		{FailoverRateLimit, 30 * time.Second},
		{FailoverOverloaded, 60 * time.Second},
		{FailoverBilling, 5 * time.Minute},
		{FailoverAuth, 10 * time.Minute},
		{FailoverAuthPermanent, 1 * time.Hour},
		{FailoverTimeout, 15 * time.Second},
		{FailoverModelNotFound, 1 * time.Hour},
		{FailoverFormat, 5 * time.Minute},
		{FailoverUnknown, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			tracker := NewCooldownTracker(512, nil)
			now := time.Now()
			tracker.nowFn = func() time.Time { return now }

			tracker.RecordFailure(cdCtx(), "openai", "test-model", tt.reason)

			// Just before cooldown expires - not available
			now = now.Add(tt.expectedDuration - 1*time.Second)
			tracker.nowFn = func() time.Time { return now } // Update closure
			if tracker.IsAvailable(cdCtx(), "openai", "test-model") {
				t.Errorf("key should not be available %v before expiry", 1*time.Second)
			}

			// After cooldown expires - available
			now = now.Add(2 * time.Second)
			tracker.nowFn = func() time.Time { return now } // Update closure
			if !tracker.IsAvailable(cdCtx(), "openai", "test-model") {
				t.Error("key should be available after cooldown expires")
			}
		})
	}
}

func TestShouldProbeInterval(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record failure to enter cooldown (FailoverTimeout = 15s, but we need longer for this test)
	// Use FailoverBilling which is 5 min to have room for probes
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverBilling) // 5 min cooldown

	// First probe immediately after failure should be allowed
	if !tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("first probe should be allowed")
	}

	// Second probe immediately after should be denied
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("second probe immediately after should be denied")
	}

	// Advance time less than minProbeInterval (30s) - still denied
	now = now.Add(20 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe before minProbeInterval should be denied")
	}

	// Advance past minProbeInterval - allowed
	now = now.Add(11 * time.Second)                 // total 31s from start
	tracker.nowFn = func() time.Time { return now } // Update closure
	if !tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe after minProbeInterval should be allowed")
	}

	// Next probe immediately after should be denied
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("next probe immediately after should be denied")
	}
}

func TestShouldProbeAfterCooldownExpires(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record failure with short cooldown
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverTimeout) // 15s

	// Probe allowed initially
	if !tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("first probe should be allowed")
	}

	// Advance past cooldown expiry
	now = now.Add(16 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure

	// Should return false after cooldown expires
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe should return false after cooldown expires")
	}
}

func TestShouldProbeNotInCooldown(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// No cooldown entry - should return false
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe should return false when not in cooldown")
	}
}

func TestShouldProbeAtomicity(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverTimeout)

	var results []bool
	var mu sync.Mutex

	// Launch multiple goroutines that all call ShouldProbe at approximately the same time
	wg := sync.WaitGroup{}
	for range 10 {
		wg.Go(func() {
			result := tracker.ShouldProbe(cdCtx(), cdProvider, cdModel)
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		})
	}
	wg.Wait()

	// Exactly one goroutine should have gotten true
	trueCount := 0
	for _, r := range results {
		if r {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Errorf("expected exactly 1 true result, got %d", trueCount)
	}
}

func TestRecordSuccessClearsCooldown(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record failure
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverBilling) // 5 min cooldown

	// Not available
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("key should not be available after failure")
	}

	// Record success - clears cooldown
	tracker.RecordSuccess(cdCtx(), cdProvider, cdModel)

	// Should be available immediately
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("key should be available after RecordSuccess")
	}

	// ShouldProbe should return false (entry deleted)
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("ShouldProbe should return false after RecordSuccess")
	}
}

func TestOverloadEscalation(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record 5 failures (at cap)
	for range 5 {
		tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverOverloaded)
		now = now.Add(1 * time.Second)                  // Increment time to allow new failures
		tracker.nowFn = func() time.Time { return now } // Update closure
	}

	// After 5th failure, should still be available at 60s (normal duration)
	now = now.Add(61 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should be available 61s after 5th failure")
	}

	// Reset to trigger 6th failure
	now = time.Now()
	tracker.nowFn = func() time.Time { return now }
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverOverloaded)
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverOverloaded) // 6th failure triggers escalation

	// Should be in cooldown at normal duration
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should not be available immediately after 6th failure")
	}

	// Advance 61 seconds - still not available due to escalation (120s)
	now = now.Add(61 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should not be available 61s after 6th failure (escalated)")
	}

	// Advance to 121 seconds total - should be available
	now = now.Add(60 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should be available 121s after 6th failure")
	}
}

// TestCooldownClampedToMax proves the phase-5 risk mitigation: whatever the
// per-reason duration and whatever the overload escalation produces, the
// in-memory deadline AND the persisted one stay at or below MaxCooldown. The
// durations are injected because no reason shipped today exceeds the ceiling —
// the clamp is the guarantee for the ones that will be added, not a no-op.
func TestCooldownClampedToMax(t *testing.T) {
	// Every reason shipped today must already fit under the clamp.
	for reason := range cooldownDurations {
		if got := CooldownDurationFor(reason); got > MaxCooldown || got <= 0 {
			t.Errorf("CooldownDurationFor(%s) = %v, want (0, %v]", reason, got, MaxCooldown)
		}
	}

	future := FailoverReason("future_reason_with_a_long_cooldown")
	cooldownDurations[future] = 48 * time.Hour
	defer delete(cooldownDurations, future)
	if got := CooldownDurationFor(future); got != MaxCooldown {
		t.Errorf("CooldownDurationFor(%s) = %v, want the %v clamp", future, got, MaxCooldown)
	}

	// The escalation branch is clamped too: an overloaded base that doubles past
	// the ceiling lands exactly on it, in memory and on the durable write.
	originalOverloaded := cooldownDurations[FailoverOverloaded]
	cooldownDurations[FailoverOverloaded] = 45 * time.Minute
	defer func() { cooldownDurations[FailoverOverloaded] = originalOverloaded }()

	store := newFakeCooldownStore()
	now := time.Now()
	tracker := NewCooldownTracker(512, store)
	tracker.nowFn = func() time.Time { return now }

	for range overloadEscalationCap + 1 {
		tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverOverloaded)
	}

	entry := tracker.entries[CooldownKey(cdProvider, cdModel)]
	if entry == nil {
		t.Fatal("expected a cooldown entry")
	}
	if got := entry.cooldownUntil.Sub(now); got != MaxCooldown {
		t.Errorf("escalated in-memory cooldown = %v, want the %v clamp", got, MaxCooldown)
	}
	recorded, ok := store.lastRecorded()
	if !ok {
		t.Fatal("expected the failure to be persisted")
	}
	if got := recorded.cooldownUntil.Sub(now); got != MaxCooldown {
		t.Errorf("persisted cooldown_until = %v past now, want the %v clamp", got, MaxCooldown)
	}
}

// TestCooldownPersistedAndHonoredAfterRestart is the phase-5 acceptance test for
// a real restart: a brand new tracker (a fresh process, empty in-memory map) over
// the SAME durable state must still refuse the provider.
func TestCooldownPersistedAndHonoredAfterRestart(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()

	before := NewCooldownTracker(512, store)
	before.nowFn = func() time.Time { return now }
	before.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverRateLimit)

	recorded, ok := store.lastRecorded()
	if !ok {
		t.Fatal("expected the failure to be persisted")
	}
	if recorded.provider != cdProvider || recorded.errorClass != string(FailoverRateLimit) {
		t.Errorf("persisted failure = %+v, want provider %q class %q", recorded, cdProvider, string(FailoverRateLimit))
	}
	if want := now.Add(30 * time.Second); !recorded.cooldownUntil.Equal(want) {
		t.Errorf("persisted cooldown_until = %v, want %v", recorded.cooldownUntil, want)
	}

	// Restart: fresh tracker, same durable state, still inside the cooldown.
	after := NewCooldownTracker(512, store)
	after.nowFn = func() time.Time { return now }
	if after.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("the durable cooldown must still be honoured after a restart")
	}
	if len(after.entries) != 1 {
		t.Errorf("expected the durable state to seed 1 in-memory entry, got %d", len(after.entries))
	}

	// And it must still expire on the persisted deadline, not forever.
	after.nowFn = func() time.Time { return now.Add(31 * time.Second) }
	if !after.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("the durable cooldown must expire on its deadline")
	}
}

// TestProbeIntervalPersistedAcrossRestart: a restarted process must not burn a
// fresh probe immediately — the probe stamp is durable too.
func TestProbeIntervalPersistedAcrossRestart(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()
	store.setCooldown(cdProvider, now.Add(10*time.Minute), now)

	after := NewCooldownTracker(512, store)
	after.nowFn = func() time.Time { return now }
	if after.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("expected the persisted cooldown to be honoured")
	}
	if after.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("a restarted process must not probe again inside the persisted probe interval")
	}

	// A second restart, still inside the interval, stays denied. IsAvailable runs
	// first because that is the call that hydrates the durable state (runOrdered
	// always asks availability before probing).
	second := NewCooldownTracker(512, store)
	second.nowFn = func() time.Time { return now }
	if second.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("expected the persisted cooldown to be honoured")
	}
	if second.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe must stay denied inside the persisted interval")
	}
	// ...and after the interval the probe is allowed again.
	second.nowFn = func() time.Time { return now.Add(minProbeInterval + time.Second) }
	if !second.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Error("probe must be allowed again after the persisted interval")
	}
}

// TestProbeStampPersistedOnce: an allowed probe is written through exactly once,
// so restarts stop re-probing.
func TestProbeStampPersistedOnce(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()
	tracker := NewCooldownTracker(512, store)
	tracker.nowFn = func() time.Time { return now }

	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverBilling)
	if !tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Fatal("first probe should be allowed")
	}
	if tracker.ShouldProbe(cdCtx(), cdProvider, cdModel) {
		t.Fatal("second probe should be denied")
	}
	if store.probeCount() != 1 {
		t.Errorf("persisted probe stamps = %d, want 1", store.probeCount())
	}
}

// TestRecordSuccessClearsPersistedCooldown: a success must clear the durable
// cooldown, otherwise the next restart resurrects it.
func TestRecordSuccessClearsPersistedCooldown(t *testing.T) {
	store := newFakeCooldownStore()
	now := time.Now()

	tracker := NewCooldownTracker(512, store)
	tracker.nowFn = func() time.Time { return now }
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverBilling)
	tracker.RecordSuccess(cdCtx(), cdProvider, cdModel)

	after := NewCooldownTracker(512, store)
	after.nowFn = func() time.Time { return now }
	if !after.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("a cleared durable cooldown must not be honoured after a restart")
	}
}

// TestCooldownStoreErrorsFailOpen: a database outage must not block LLM traffic.
// A failed read reports the key available (and logs), a failed write keeps the
// in-memory cooldown working.
func TestCooldownStoreErrorsFailOpen(t *testing.T) {
	store := newFakeCooldownStore()
	store.readErr = errors.New("database is down")
	now := time.Now()

	tracker := NewCooldownTracker(512, store)
	tracker.nowFn = func() time.Time { return now }
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("a failed durable read must fail open (available)")
	}

	store.readErr = nil
	store.writeErr = errors.New("database is down")
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverRateLimit)
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("a failed durable write must still leave the in-memory cooldown active")
	}
}

// TestDurableReadIsCached: a healthy provider must not cost one durable read per
// request — the hydrated entry is the negative cache.
func TestDurableReadIsCached(t *testing.T) {
	store := newFakeCooldownStore()
	tracker := NewCooldownTracker(512, store)

	for range 5 {
		if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
			t.Fatal("provider without durable state must be available")
		}
	}
	store.mu.Lock()
	reads := len(store.reads)
	store.mu.Unlock()
	if reads != 1 {
		t.Errorf("durable reads = %d, want 1 (subsequent checks answer from memory)", reads)
	}
}

func TestMaxKeyEviction(t *testing.T) {
	maxKeys := 3
	tracker := NewCooldownTracker(maxKeys, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Add keys up to max
	for i := range maxKeys {
		tracker.RecordFailure(cdCtx(), "openai", "model-"+string(rune(i)), FailoverTimeout)
		now = now.Add(1 * time.Second)                  // Increment to make createdAt different
		tracker.nowFn = func() time.Time { return now } // Update closure
	}

	if len(tracker.entries) != maxKeys {
		t.Errorf("expected %d entries, got %d", maxKeys, len(tracker.entries))
	}

	// Add one more key - should evict oldest
	key4 := CooldownKey("openai", "model-4")
	tracker.RecordFailure(cdCtx(), "openai", "model-4", FailoverTimeout)

	if len(tracker.entries) != maxKeys {
		t.Errorf("expected %d entries after eviction, got %d", maxKeys, len(tracker.entries))
	}

	// Oldest key (model-0) should have been evicted
	key0 := CooldownKey("openai", "model-0")
	if tracker.IsAvailable(cdCtx(), "openai", "model-0") && len(tracker.entries) == maxKeys {
		// If key0 is available and we still have maxKeys entries, key0 was evicted
		// Check that it's not in entries
		if _, exists := tracker.entries[key0]; exists {
			t.Error("oldest key should have been evicted")
		}
	}

	// New key should be in entries
	if _, exists := tracker.entries[key4]; !exists {
		t.Error("new key should be in entries")
	}
}

func TestTTLCleanup(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Add an old entry
	key1 := CooldownKey("openai", "old-model")
	tracker.RecordFailure(cdCtx(), "openai", "old-model", FailoverTimeout)

	// Add a fresh entry (will trigger cleanup during next RecordFailure)
	now = now.Add(25 * time.Hour)                   // Past TTL (24h)
	tracker.nowFn = func() time.Time { return now } // Update closure
	key2 := CooldownKey("openai", "new-model")
	tracker.RecordFailure(cdCtx(), "openai", "new-model", FailoverTimeout)

	// Old entry should have been cleaned up
	if _, exists := tracker.entries[key1]; exists {
		t.Error("old entry should have been cleaned up by TTL")
	}

	// New entry should still exist
	if _, exists := tracker.entries[key2]; !exists {
		t.Error("new entry should still exist")
	}
}

func TestConcurrentAccess(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	keys := [][2]string{
		{"openai", "gpt-4"},
		{"anthropic", "claude-3-opus"},
		{"groq", "mixtral-8x7b"},
	}

	wg := sync.WaitGroup{}

	// Concurrent RecordFailure and IsAvailable
	for range 10 {
		for _, key := range keys {
			wg.Add(1)
			go func(provider, model string) {
				defer wg.Done()
				tracker.RecordFailure(cdCtx(), provider, model, FailoverTimeout)
				_ = tracker.IsAvailable(cdCtx(), provider, model)
				_ = tracker.ShouldProbe(cdCtx(), provider, model)
				tracker.RecordSuccess(cdCtx(), provider, model)
			}(key[0], key[1])
		}
	}

	wg.Wait()

	// All keys should have been cleaned up by RecordSuccess
	if len(tracker.entries) != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", len(tracker.entries))
	}
}

func TestMultipleFailureReasons(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record first failure
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverRateLimit)
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should not be available after rate_limit failure")
	}

	// Advance to next probe window
	now = now.Add(35 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure

	// Record different failure reason during cooldown (updates reason, resets cooldown)
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverAuth) // 10 min

	// Should not be available due to new auth cooldown
	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should not be available, auth cooldown is set")
	}

	// Advance past auth cooldown (10 min = 600s)
	now = now.Add(601 * time.Second)
	tracker.nowFn = func() time.Time { return now } // Update closure
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should be available after auth cooldown expires")
	}
}

func TestEmptyKey(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Should handle empty provider/model without panic
	tracker.RecordFailure(cdCtx(), "", "", FailoverTimeout)
	if tracker.IsAvailable(cdCtx(), "", "") {
		t.Error("empty key should not be available after failure")
	}
	tracker.RecordSuccess(cdCtx(), "", "")
	if !tracker.IsAvailable(cdCtx(), "", "") {
		t.Error("empty key should be available after success")
	}
}

func TestUnknownFailoverReason(t *testing.T) {
	tracker := NewCooldownTracker(512, nil)
	now := time.Now()
	tracker.nowFn = func() time.Time { return now }

	// Record with unknown reason - should use default 30s
	tracker.RecordFailure(cdCtx(), cdProvider, cdModel, FailoverReason("unknown_future_reason"))

	if tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should not be available after unknown reason failure")
	}

	now = now.Add(31 * time.Second)
	if !tracker.IsAvailable(cdCtx(), cdProvider, cdModel) {
		t.Error("should be available after default 30s cooldown")
	}
}
