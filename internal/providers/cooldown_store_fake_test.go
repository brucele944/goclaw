package providers

import (
	"context"
	"sync"
	"time"
)

// fakeCooldownStore is an in-memory stand-in for the durable half of provider
// cooldown state (store.provider_health, migration 000100). Its whole point is
// that it OUTLIVES a CooldownTracker: the restart tests build a second tracker
// over the same fake and require the persisted cooldown to still be honoured.
//
// The fake mirrors the real store's semantics exactly:
//   - RecordFailure stores the cooldown deadline (and the error class/count) and
//     does NOT touch the probe stamp;
//   - MarkProbe stores the probe stamp and does NOT touch the cooldown;
//   - ClearFailure drops the cooldown but keeps the error-class history;
//   - a provider that was never written has no state (Method 2 == false).
type fakeCooldownStore struct {
	mu sync.Mutex

	cooldownUntil map[string]time.Time
	lastProbe     map[string]time.Time
	failures      map[string]int
	errorCounts   map[string]map[string]int

	// recorded counters, for assertions
	recordedFailures []fakeFailure
	probes           []string
	clears           []string
	reads            []string

	writeErr error
	readErr  error
}

type fakeFailure struct {
	provider      string
	errorClass    string
	cooldownUntil time.Time
}

func newFakeCooldownStore() *fakeCooldownStore {
	return &fakeCooldownStore{
		cooldownUntil: map[string]time.Time{},
		lastProbe:     map[string]time.Time{},
		failures:      map[string]int{},
		errorCounts:   map[string]map[string]int{},
	}
}

func (f *fakeCooldownStore) CooldownState(_ context.Context, providerName string) (CooldownState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, providerName)
	if f.readErr != nil {
		return CooldownState{}, false, f.readErr
	}
	until, hasCooldown := f.cooldownUntil[providerName]
	probeAt, hasProbe := f.lastProbe[providerName]
	failureCount := f.failures[providerName]
	if !hasCooldown && !hasProbe && failureCount == 0 {
		return CooldownState{}, false, nil
	}
	return CooldownState{
		CooldownUntil:       until,
		LastProbe:           probeAt,
		ConsecutiveFailures: failureCount,
	}, true, nil
}

func (f *fakeCooldownStore) RecordFailure(_ context.Context, providerName, errorClass string, cooldownUntil time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.recordedFailures = append(f.recordedFailures, fakeFailure{
		provider: providerName, errorClass: errorClass, cooldownUntil: cooldownUntil,
	})
	f.cooldownUntil[providerName] = cooldownUntil
	f.failures[providerName]++
	if f.errorCounts[providerName] == nil {
		f.errorCounts[providerName] = map[string]int{}
	}
	f.errorCounts[providerName][errorClass]++
	return nil
}

func (f *fakeCooldownStore) MarkProbe(_ context.Context, providerName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.probes = append(f.probes, providerName)
	if f.lastProbe == nil {
		f.lastProbe = map[string]time.Time{}
	}
	f.lastProbe[providerName] = time.Now()
	return nil
}

func (f *fakeCooldownStore) ClearFailure(_ context.Context, providerName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.clears = append(f.clears, providerName)
	delete(f.cooldownUntil, providerName)
	f.failures[providerName] = 0
	return nil
}

// lastRecorded returns the most recent persisted failure, if any.
func (f *fakeCooldownStore) lastRecorded() (fakeFailure, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.recordedFailures) == 0 {
		return fakeFailure{}, false
	}
	return f.recordedFailures[len(f.recordedFailures)-1], true
}

// probeCount returns how many probe stamps were persisted.
func (f *fakeCooldownStore) probeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.probes)
}

// setCooldown seeds a persisted cooldown directly, i.e. the state a previous
// process left behind. probeAt may be the zero time for "never probed".
func (f *fakeCooldownStore) setCooldown(providerName string, until, probeAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cooldownUntil[providerName] = until
	if !probeAt.IsZero() {
		f.lastProbe[providerName] = probeAt
	}
	f.failures[providerName] = 1
}
