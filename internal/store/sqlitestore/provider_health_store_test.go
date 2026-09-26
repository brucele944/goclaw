//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteProviderHealthCRUD covers the durable cooldown store (migration
// 000100): the failure counters/histogram, cooldown state, probe stamp, success
// clearing, and the operator reset — plus the parent-tenant guard on reset and
// the FK cascade when the provider goes away.
func TestSQLiteProviderHealthCRUD(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	st := NewSQLiteProviderStore(db, "")
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)

	provider := &store.LLMProviderData{
		Name:         "health-a",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	// No row yet: a provider that never failed reads as a zero health, not an error.
	health, err := st.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth on a fresh provider: %v", err)
	}
	if health.ConsecutiveFailures != 0 || health.CooldownUntil != nil || health.LastProbeAt != nil {
		t.Errorf("fresh provider health = %+v, want zero", health)
	}
	if len(health.ErrorCounts) != 0 {
		t.Errorf("fresh provider error counts = %+v, want empty", health.ErrorCounts)
	}
	if !health.UpdatedAt.IsZero() {
		t.Errorf("fresh provider UpdatedAt = %v, want the zero time (no row)", health.UpdatedAt)
	}

	// Three failures across two classes.
	deadline := time.Now().UTC().Add(90 * time.Second).Truncate(time.Millisecond)
	for _, class := range []string{"rate_limit", "rate_limit", "timeout"} {
		if err := st.RecordProviderFailure(ctx, provider.ID, class, deadline); err != nil {
			t.Fatalf("RecordProviderFailure(%s): %v", class, err)
		}
	}

	health, err = st.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth: %v", err)
	}
	if health.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", health.ConsecutiveFailures)
	}
	if health.CooldownUntil == nil || !health.CooldownUntil.Equal(deadline) {
		t.Errorf("CooldownUntil = %v, want %v", health.CooldownUntil, deadline)
	}
	if health.LastErrorClass != "timeout" {
		t.Errorf("LastErrorClass = %q, want timeout", health.LastErrorClass)
	}
	if health.ErrorCounts["rate_limit"] != 2 || health.ErrorCounts["timeout"] != 1 {
		t.Errorf("ErrorCounts = %+v, want rate_limit=2 timeout=1", health.ErrorCounts)
	}
	if !health.CoolingDown(time.Now().UTC()) {
		t.Error("CoolingDown(now) = false, want true while the deadline is in the future")
	}

	// The error class is normalized before it becomes a histogram bucket.
	if err := st.RecordProviderFailure(ctx, provider.ID, "RATE LIMIT!!", deadline); err != nil {
		t.Fatalf("RecordProviderFailure(unnormalized): %v", err)
	}
	health, _ = st.GetProviderHealth(ctx, provider.ID)
	if health.ErrorCounts[store.ErrorClassUnknown] != 1 {
		t.Errorf("ErrorCounts = %+v, want an %q bucket for the unnormalized class", health.ErrorCounts, store.ErrorClassUnknown)
	}

	// A success clears the live cooldown, keeps the failure history.
	if err := st.RecordProviderSuccess(ctx, provider.ID); err != nil {
		t.Fatalf("RecordProviderSuccess: %v", err)
	}
	health, _ = st.GetProviderHealth(ctx, provider.ID)
	if health.ConsecutiveFailures != 0 || health.CooldownUntil != nil {
		t.Errorf("after success health = %+v, want failures 0 and no cooldown", health)
	}
	if health.LastErrorClass != store.ErrorClassUnknown {
		t.Errorf("LastErrorClass = %q, want the last recorded class %q", health.LastErrorClass, store.ErrorClassUnknown)
	}
	if health.ErrorCounts["timeout"] != 1 || health.ErrorCounts["rate_limit"] != 2 {
		t.Errorf("after success ErrorCounts = %+v, want the histogram retained", health.ErrorCounts)
	}

	// The probe stamp is recorded without touching the failure counters.
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

	// The state is durable, not process-local: the row is in the DB.
	var storedFailures int
	if err := db.QueryRow(`SELECT consecutive_failures FROM provider_health WHERE provider_id = ?`, provider.ID).Scan(&storedFailures); err != nil {
		t.Fatalf("read provider_health row: %v", err)
	}
	if storedFailures != 0 {
		t.Errorf("persisted consecutive_failures = %d, want 0", storedFailures)
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
	otherTenant := uuid.Must(uuid.NewV7())
	mustExec(t, db, `INSERT INTO tenants (id, name, slug, status) VALUES (?, 'Tenant X', ?, 'active')`,
		otherTenant, "tenant-x-"+otherTenant.String())
	if err := st.ResetProviderHealth(store.WithTenantID(context.Background(), otherTenant), provider.ID); err == nil {
		t.Error("ResetProviderHealth from another tenant returned nil, want the provider-not-found guard")
	}

	// Deleting the provider cascades the health rows away.
	if err := st.RecordProviderFailure(ctx, provider.ID, "billing", deadline); err != nil {
		t.Fatalf("RecordProviderFailure before delete: %v", err)
	}
	if err := st.DeleteProvider(ctx, provider.ID); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_health WHERE provider_id = ?`, provider.ID).Scan(&remaining); err != nil {
		t.Fatalf("count provider_health rows: %v", err)
	}
	if remaining != 0 {
		t.Errorf("provider_health rows after provider delete = %d, want 0 (FK cascade)", remaining)
	}
	var remainingCounts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_error_counts WHERE provider_id = ?`, provider.ID).Scan(&remainingCounts); err != nil {
		t.Fatalf("count provider_error_counts rows: %v", err)
	}
	if remainingCounts != 0 {
		t.Errorf("provider_error_counts rows after provider delete = %d, want 0 (FK cascade)", remainingCounts)
	}
}

// TestSQLiteProviderHealthSurvivesReopen proves the durability claim the whole
// feature rests on: a second store handle over the same database file sees the
// cooldown a previous one recorded — the in-memory tracker is not the source of
// truth across a restart.
func TestSQLiteProviderHealthSurvivesReopen(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	first := NewSQLiteProviderStore(db, "")

	provider := &store.LLMProviderData{
		Name:         "health-reopen",
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := first.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	deadline := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)
	if err := first.RecordProviderFailure(ctx, provider.ID, "overloaded", deadline); err != nil {
		t.Fatalf("RecordProviderFailure: %v", err)
	}

	// A fresh handle ("restarted process") over the same DB file.
	second := NewSQLiteProviderStore(db, "")
	health, err := second.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth from the second handle: %v", err)
	}
	if health.CooldownUntil == nil || !health.CooldownUntil.Equal(deadline) {
		t.Fatalf("cooldown after reopen = %v, want %v (a restart must not forget it)", health.CooldownUntil, deadline)
	}
	if health.ConsecutiveFailures != 1 || health.LastErrorClass != "overloaded" {
		t.Errorf("health after reopen = %+v, want 1 failure classified overloaded", health)
	}
}
