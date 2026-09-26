package pg

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestPGProviderHealthCRUD mirrors the SQLite provider-health test against a live
// Postgres (migration 000100) so the two backends cannot drift: failure counters,
// error-class histogram, cooldown state, probe stamp, success clearing, the
// operator reset, the parent-tenant guard on reset and the FK cascade.
func TestPGProviderHealthCRUD(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider health store tests")
	}
	db := hooksTestDB(t)
	st := NewPGProviderStore(db, "")

	ctx := masterCtx()
	provider := &store.LLMProviderData{
		Name:         "pg-health-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM provider_health WHERE provider_id = $1`, provider.ID)
		db.Exec(`DELETE FROM llm_providers WHERE id = $1`, provider.ID)
	})

	// No row yet: a provider that never failed reads as a zero health.
	health, err := st.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth on a fresh provider: %v", err)
	}
	if health.ConsecutiveFailures != 0 || health.CooldownUntil != nil || health.LastProbeAt != nil ||
		len(health.ErrorCounts) != 0 || !health.UpdatedAt.IsZero() {
		t.Errorf("fresh provider health = %+v, want the zero value", health)
	}

	deadline := time.Now().UTC().Add(90 * time.Second).Truncate(time.Millisecond)
	for _, class := range []string{"rate_limit", "rate_limit", "timeout"} {
		if err := st.RecordProviderFailure(ctx, provider.ID, class, deadline); err != nil {
			t.Fatalf("RecordProviderFailure(%s): %v", class, err)
		}
	}
	// An unnormalized class must land in the shared "unknown" bucket.
	if err := st.RecordProviderFailure(ctx, provider.ID, "RATE LIMIT!!", deadline); err != nil {
		t.Fatalf("RecordProviderFailure(unnormalized): %v", err)
	}

	health, err = st.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth: %v", err)
	}
	if health.ConsecutiveFailures != 4 {
		t.Errorf("ConsecutiveFailures = %d, want 4", health.ConsecutiveFailures)
	}
	if health.CooldownUntil == nil || !health.CooldownUntil.Equal(deadline) {
		t.Errorf("CooldownUntil = %v, want %v", health.CooldownUntil, deadline)
	}
	if health.LastErrorClass != store.ErrorClassUnknown {
		t.Errorf("LastErrorClass = %q, want %q", health.LastErrorClass, store.ErrorClassUnknown)
	}
	if health.ErrorCounts["rate_limit"] != 2 || health.ErrorCounts["timeout"] != 1 || health.ErrorCounts[store.ErrorClassUnknown] != 1 {
		t.Errorf("ErrorCounts = %+v, want rate_limit=2 timeout=1 unknown=1", health.ErrorCounts)
	}

	// Durability: the state is in the tables, not in process memory.
	var storedFailures int
	if err := db.QueryRow(`SELECT consecutive_failures FROM provider_health WHERE provider_id = $1`, provider.ID).Scan(&storedFailures); err != nil {
		t.Fatalf("read provider_health row: %v", err)
	}
	if storedFailures != 4 {
		t.Errorf("persisted consecutive_failures = %d, want 4", storedFailures)
	}

	// A success clears the live cooldown and keeps the failure history.
	if err := st.RecordProviderSuccess(ctx, provider.ID); err != nil {
		t.Fatalf("RecordProviderSuccess: %v", err)
	}
	health, _ = st.GetProviderHealth(ctx, provider.ID)
	if health.ConsecutiveFailures != 0 || health.CooldownUntil != nil {
		t.Errorf("after success health = %+v, want failures 0 and no cooldown", health)
	}
	if health.ErrorCounts["rate_limit"] != 2 {
		t.Errorf("histogram after success = %+v, want it retained", health.ErrorCounts)
	}

	// Probe stamp, without touching the counters.
	if err := st.MarkProviderProbe(ctx, provider.ID); err != nil {
		t.Fatalf("MarkProviderProbe: %v", err)
	}
	health, _ = st.GetProviderHealth(ctx, provider.ID)
	if health.LastProbeAt == nil {
		t.Fatal("LastProbeAt = nil after MarkProviderProbe")
	}
	if health.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d after a probe, want 0", health.ConsecutiveFailures)
	}

	// Reset (the CLI escape hatch) clears cooldown, counters and histogram.
	if err := st.ResetProviderHealth(ctx, provider.ID); err != nil {
		t.Fatalf("ResetProviderHealth: %v", err)
	}
	health, _ = st.GetProviderHealth(ctx, provider.ID)
	if !health.UpdatedAt.IsZero() || health.ConsecutiveFailures != 0 || health.LastProbeAt != nil || len(health.ErrorCounts) != 0 {
		t.Errorf("after reset health = %+v, want the zero value", health)
	}

	// Reset is a write on a provider-scoped table: another tenant must not reach it.
	tenantBID, _ := seedTenantAndAgent(t, db)
	if err := st.ResetProviderHealth(tenantScopedCtx(tenantBID), provider.ID); err == nil {
		t.Error("ResetProviderHealth from another tenant returned nil, want the provider-not-found guard")
	}

	// Deleting the provider cascades both health tables away.
	if err := st.RecordProviderFailure(ctx, provider.ID, "billing", deadline); err != nil {
		t.Fatalf("RecordProviderFailure before delete: %v", err)
	}
	if err := st.DeleteProvider(ctx, provider.ID); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	for _, table := range []string{"provider_health", "provider_error_counts"} {
		var remaining int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE provider_id = $1`, provider.ID).Scan(&remaining); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if remaining != 0 {
			t.Errorf("%s rows after provider delete = %d, want 0 (FK cascade)", table, remaining)
		}
	}
}

// TestPGProviderHealthSurvivesReconnect is the Postgres half of the restart
// simulation: a separate store handle over the same database reads back the
// cooldown a previous handle recorded. The durable table (asserted with raw SQL
// above) is what makes the cooldown outlive a gateway restart — the in-memory
// tracker is a cache, not the source of truth.
func TestPGProviderHealthSurvivesReconnect(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider health store tests")
	}
	db := hooksTestDB(t)
	ctx := masterCtx()

	first := NewPGProviderStore(db, "")
	provider := &store.LLMProviderData{
		Name:         "pg-health-reopen-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := first.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM provider_health WHERE provider_id = $1`, provider.ID)
		db.Exec(`DELETE FROM llm_providers WHERE id = $1`, provider.ID)
	})

	deadline := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)
	if err := first.RecordProviderFailure(ctx, provider.ID, "overloaded", deadline); err != nil {
		t.Fatalf("RecordProviderFailure: %v", err)
	}

	// The cooldown is a row in the database, not process memory.
	var storedCooldown time.Time
	if err := db.QueryRow(`SELECT cooldown_until FROM provider_health WHERE provider_id = $1`, provider.ID).Scan(&storedCooldown); err != nil {
		t.Fatalf("read persisted cooldown_until: %v", err)
	}
	if !storedCooldown.Equal(deadline) {
		t.Errorf("persisted cooldown_until = %v, want %v", storedCooldown, deadline)
	}

	// A fresh store handle ("restarted process") reads the same state back.
	second := NewPGProviderStore(db, "")
	health, err := second.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth from the second handle: %v", err)
	}
	if health.CooldownUntil == nil || !health.CooldownUntil.Equal(deadline) {
		t.Fatalf("cooldown after restart = %v, want %v (a restart must not forget it)", health.CooldownUntil, deadline)
	}
	if health.ConsecutiveFailures != 1 || health.LastErrorClass != "overloaded" {
		t.Errorf("health after restart = %+v, want 1 failure classified overloaded", health)
	}
}
