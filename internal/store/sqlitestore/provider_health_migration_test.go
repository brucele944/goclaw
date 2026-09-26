//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteSchemaUpgrade_62_to_63_ProviderHealth verifies the v62 → v63
// migration mirrors migrations/000100_provider_health.up.sql: both tables exist
// afterwards, the recorded schema version advances, and the new tables behave
// (upsert, histogram increment, FK cascade).
func TestSQLiteSchemaUpgrade_62_to_63_ProviderHealth(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// openTestDB applies the LATEST schema; drop the v63 tables so EnsureSchema
	// exercises the real v62 → v63 patch, then reset the recorded version.
	mustExec(t, db, `DROP TABLE provider_error_counts`)
	mustExec(t, db, `DROP TABLE provider_health`)
	mustExec(t, db, `UPDATE schema_version SET version = 62`)

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema (v62→63) failed: %v", err)
	}

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if SchemaVersion < 63 {
		t.Fatalf("SchemaVersion = %d, want >= 63", SchemaVersion)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
	if tableSQL(t, db, "provider_health") == "" {
		t.Fatal("provider_health missing after v62 → v63")
	}
	if tableSQL(t, db, "provider_error_counts") == "" {
		t.Fatal("provider_error_counts missing after v62 → v63")
	}

	// The fresh tables work through the store: upsert, histogram, cascade.
	initSqlx(db)
	st := NewSQLiteProviderStore(db, "")
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)

	provider := &store.LLMProviderData{
		Name:         "health-mig-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderOpenAICompat,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM llm_providers WHERE id = ?`, provider.ID) })

	deadline := time.Now().UTC().Add(time.Minute)
	for range 2 {
		if err := st.RecordProviderFailure(ctx, provider.ID, "timeout", deadline); err != nil {
			t.Fatalf("RecordProviderFailure: %v", err)
		}
	}
	health, err := st.GetProviderHealth(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProviderHealth: %v", err)
	}
	if health.ConsecutiveFailures != 2 || health.ErrorCounts["timeout"] != 2 {
		t.Fatalf("health after two failures = %+v, want 2 failures and timeout=2", health)
	}

	mustExec(t, db, `DELETE FROM llm_providers WHERE id = ?`, provider.ID)
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_health WHERE provider_id = ?`, provider.ID).Scan(&remaining); err != nil {
		t.Fatalf("count provider_health: %v", err)
	}
	if remaining != 0 {
		t.Errorf("provider_health rows after provider delete = %d, want 0 (FK cascade)", remaining)
	}
}
