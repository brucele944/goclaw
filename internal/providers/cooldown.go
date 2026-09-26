package providers

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// CooldownState is the durable cooldown state of one provider, as persisted by
// a CooldownStore. Zero times mean "not persisted".
type CooldownState struct {
	CooldownUntil       time.Time
	LastProbe           time.Time
	ConsecutiveFailures int
}

// CooldownStore is the durable half of provider cooldown state (provider_health,
// migration 000100). The runtime path only knows provider *names* — that is all
// a FallbackCandidate carries — so this interface is name-keyed; adapters
// resolve a name to the provider row. The in-memory map in CooldownTracker stays
// the fast path, the store is what survives a restart.
type CooldownStore interface {
	// CooldownState returns the persisted state of one provider. ok=false means
	// nothing is persisted (the provider never failed).
	CooldownState(ctx context.Context, providerName string) (state CooldownState, ok bool, err error)
	// RecordFailure persists a failure: the error class and the new cooldown
	// deadline (already clamped to MaxCooldown by the caller).
	RecordFailure(ctx context.Context, providerName, errorClass string, cooldownUntil time.Time) error
	// MarkProbe records that the provider was probed at now, so a restarted
	// process does not immediately burn another probe.
	MarkProbe(ctx context.Context, providerName string) error
	// ClearFailure clears the persisted cooldown of one provider after a success.
	ClearFailure(ctx context.Context, providerName string) error
}

// CooldownTracker tracks per-provider:model failure state with decay and probe intervals.
// Thread-safe. The in-memory map is the fast path; when a CooldownStore is
// attached, state is read back from it on first use of a key (so a restart no
// longer forgets an active cooldown) and written through on every
// failure/probe/success.
type CooldownTracker struct {
	mu          sync.Mutex
	entries     map[string]*cooldownEntry
	maxKeys     int
	lastCleanup time.Time        // amortize TTL cleanup
	nowFn       func() time.Time // for testing; defaults to time.Now
	store       CooldownStore    // nil = in-memory only (pre-phase-5 behaviour)
}

type cooldownEntry struct {
	reason         FailoverReason
	cooldownUntil  time.Time
	lastProbe      time.Time
	failureCount   int
	overloadStreak int // consecutive overloaded failures (resets on different reason)
	createdAt      time.Time
}

// Cooldown durations by failure reason.
var cooldownDurations = map[FailoverReason]time.Duration{
	FailoverRateLimit:     30 * time.Second,
	FailoverOverloaded:    60 * time.Second,
	FailoverBilling:       5 * time.Minute,
	FailoverAuth:          10 * time.Minute,
	FailoverAuthPermanent: 1 * time.Hour,
	FailoverTimeout:       15 * time.Second,
	FailoverModelNotFound: 1 * time.Hour,
	FailoverFormat:        5 * time.Minute,
	FailoverUnknown:       30 * time.Second,
}

const (
	minProbeInterval      = 30 * time.Second
	stateTTL              = 24 * time.Hour
	overloadEscalationCap = 5 // after 5 consecutive overloaded failures, double cooldown
	defaultMaxKeys        = 512

	// MaxCooldown bounds how long one provider can stay out of rotation,
	// whatever the failure reason. The longest per-reason cooldown is 1h
	// (auth_permanent, model_not_found) and the overload escalation doubles to
	// 2h, so this clamp bites only on the escalation path. Phase 5 flags
	// unbounded cooldown as a risk: a provider misclassified as permanently
	// broken must not be wedged until an operator notices, and the persistent
	// store makes a wrong cooldown outlive the process that recorded it. The
	// clamp applies to the in-memory deadline and to the persisted one.
	MaxCooldown = time.Hour
)

// NewCooldownTracker creates a tracker with a max key limit. store may be nil
// (in-memory only); otherwise it is the durable state the tracker reads back on
// first use of a key and writes through on every change.
func NewCooldownTracker(maxKeys int, store CooldownStore) *CooldownTracker {
	if maxKeys <= 0 {
		maxKeys = defaultMaxKeys
	}
	return &CooldownTracker{
		entries: make(map[string]*cooldownEntry),
		maxKeys: maxKeys,
		nowFn:   time.Now,
		store:   store,
	}
}

// CooldownKey builds a cooldown lookup key from provider and model.
func CooldownKey(provider, model string) string {
	return provider + ":" + model
}

// CooldownDurationFor returns the cooldown for a failover reason, clamped to
// MaxCooldown. Exported so the health surface computes a persisted deadline for
// a failed active probe with exactly the runtime rules.
func CooldownDurationFor(reason FailoverReason) time.Duration {
	duration := cooldownDurations[reason]
	if duration <= 0 {
		duration = cooldownDurations[FailoverUnknown]
	}
	if duration > MaxCooldown {
		duration = MaxCooldown
	}
	return duration
}

// RecordFailure records a provider error, enters cooldown with the
// reason-appropriate duration and writes the state through to the durable store.
func (t *CooldownTracker) RecordFailure(ctx context.Context, providerName, model string, reason FailoverReason) {
	key := CooldownKey(providerName, model)
	deadline := t.recordFailureLocked(key, reason)
	if t.store == nil {
		return
	}
	t.persist(ctx, providerName, "failure", func() error {
		return t.store.RecordFailure(ctx, providerName, string(reason), deadline)
	})
}

// recordFailureLocked updates the in-memory entry and returns the new deadline.
func (t *CooldownTracker) recordFailureLocked(key string, reason FailoverReason) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.nowFn()
	// Amortize TTL cleanup: only scan every 5 minutes to avoid O(n) on every call
	if now.Sub(t.lastCleanup) > 5*time.Minute {
		t.cleanupLocked(now)
		t.lastCleanup = now
	}

	entry, exists := t.entries[key]
	if !exists {
		if len(t.entries) >= t.maxKeys {
			t.evictOldest()
		}
		entry = &cooldownEntry{createdAt: now}
		t.entries[key] = entry
	}

	// Track consecutive overload streak (resets on different reason)
	if reason == FailoverOverloaded {
		entry.overloadStreak++
	} else {
		entry.overloadStreak = 0
	}
	entry.reason = reason
	entry.failureCount++

	duration := CooldownDurationFor(reason)

	// Overload escalation: flat 2x cooldown after cap consecutive overloaded failures.
	// Intentionally flat (not exponential) to avoid overly long cooldowns, and
	// clamped by MaxCooldown so the escalation cannot outlive the ceiling.
	if reason == FailoverOverloaded && entry.overloadStreak > overloadEscalationCap {
		duration *= 2
		if duration > MaxCooldown {
			duration = MaxCooldown
		}
	}

	entry.cooldownUntil = now.Add(duration)
	return entry.cooldownUntil
}

// IsAvailable returns true if the key is not in active cooldown. The first look
// at a key consults the durable store (if attached), so a cooldown recorded
// before a restart is still honoured; after that the in-memory entry answers.
// A store error fails open — a database outage must not block every LLM call —
// and logs at WARN so the loss of durability stays observable.
func (t *CooldownTracker) IsAvailable(ctx context.Context, providerName, model string) bool {
	key := CooldownKey(providerName, model)

	t.mu.Lock()
	entry, exists := t.entries[key]
	if exists {
		available := t.nowFn().After(entry.cooldownUntil)
		t.mu.Unlock()
		return available
	}
	t.mu.Unlock()

	if t.store == nil {
		return true
	}
	return t.hydrate(ctx, key, providerName)
}

// hydrate seeds an in-memory entry from the durable state of a key that has no
// entry yet, and reports whether the key is available. Recording the entry even
// when nothing is persisted is the negative cache: a healthy provider costs one
// durable read per process (or per stateTTL eviction), not one per request.
func (t *CooldownTracker) hydrate(ctx context.Context, key, providerName string) bool {
	now := t.nowFn()
	state, ok, err := t.store.CooldownState(ctx, providerName)
	if err != nil {
		slog.Warn("providers.cooldown_read_failed", "provider", providerName, "error", err)
		return true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	entry, exists := t.entries[key]
	if !exists {
		if len(t.entries) >= t.maxKeys {
			t.evictOldest()
		}
		entry = &cooldownEntry{createdAt: now}
		t.entries[key] = entry
	}
	if !ok {
		return true
	}
	// The durable deadline is authoritative for a key with no local state: it may
	// be shorter than what this process remembers (another instance recorded a
	// success) but it must never resurrect a cooldown the store already cleared.
	entry.cooldownUntil = state.CooldownUntil
	entry.lastProbe = state.LastProbe
	if state.ConsecutiveFailures > entry.failureCount {
		entry.failureCount = state.ConsecutiveFailures
	}
	return entry.cooldownUntil.IsZero() || now.After(entry.cooldownUntil)
}

// ShouldProbe returns true if a probe request is allowed during cooldown.
// Atomically updates lastProbe so only one caller per interval gets true, and
// persists the stamp so a restarted process does not probe again immediately.
func (t *CooldownTracker) ShouldProbe(ctx context.Context, providerName, model string) bool {
	key := CooldownKey(providerName, model)

	t.mu.Lock()
	entry, exists := t.entries[key]
	if !exists {
		t.mu.Unlock()
		return false // not in cooldown
	}

	now := t.nowFn()
	if now.After(entry.cooldownUntil) {
		t.mu.Unlock()
		return false // cooldown expired
	}

	allowed := now.Sub(entry.lastProbe) >= minProbeInterval
	if allowed {
		entry.lastProbe = now
	}
	t.mu.Unlock()

	if allowed && t.store != nil {
		t.persist(ctx, providerName, "probe", func() error {
			return t.store.MarkProbe(ctx, providerName)
		})
	}
	return allowed
}

// RecordSuccess clears cooldown immediately for a key, in memory and durably.
func (t *CooldownTracker) RecordSuccess(ctx context.Context, providerName, model string) {
	key := CooldownKey(providerName, model)
	t.mu.Lock()
	delete(t.entries, key)
	t.mu.Unlock()

	if t.store == nil {
		return
	}
	t.persist(ctx, providerName, "success", func() error {
		return t.store.ClearFailure(ctx, providerName)
	})
}

// persist runs one durable write outside the tracker lock and logs a failure.
// A lost write costs durability, never the request: the in-memory state the
// caller already updated stays correct for this process.
func (t *CooldownTracker) persist(ctx context.Context, providerName, reason string, write func() error) {
	if err := write(); err != nil {
		slog.Warn("providers.cooldown_persist_failed",
			"provider", providerName, "reason", reason, "error", err)
	}
}

// cleanupLocked removes entries older than stateTTL. Must hold mu.
func (t *CooldownTracker) cleanupLocked(now time.Time) {
	for key, entry := range t.entries {
		if now.Sub(entry.createdAt) > stateTTL {
			delete(t.entries, key)
		}
	}
}

// evictOldest removes the oldest entry by createdAt. Must hold mu.
func (t *CooldownTracker) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for key, entry := range t.entries {
		if first || entry.createdAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.createdAt
			first = false
		}
	}
	if oldestKey != "" {
		delete(t.entries, oldestKey)
	}
}
