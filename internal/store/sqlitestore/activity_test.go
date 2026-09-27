//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteActivityStoreReadsDetailsJSON pins the SQLite contract of the activity
// log: `details` is a JSON column stored as TEXT, and database/sql cannot scan a
// string into json.RawMessage — the list query used to fail with a scan error,
// turning GET /v1/activity into a 500 whenever the log was non-empty.
func TestSQLiteActivityStoreReadsDetailsJSON(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO activity_logs (id, actor_type, actor_id, action, entity_type, entity_id, details, ip_address, tenant_id)
		 VALUES (?, 'user', 'user-1', 'agent.update', 'agent', 'agent-1', '{"field":"model"}', '127.0.0.1', ?)`,
		uuid.New().String(), store.MasterTenantID.String(),
	); err != nil {
		t.Fatalf("seed activity log: %v", err)
	}
	// A NULL details column must not abort the row either.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO activity_logs (id, actor_type, actor_id, action, tenant_id)
		 VALUES (?, 'user', 'user-1', 'agent.create', ?)`,
		uuid.New().String(), store.MasterTenantID.String(),
	); err != nil {
		t.Fatalf("seed null-details row: %v", err)
	}

	logs, err := NewSQLiteActivityStore(db).List(ctx, store.ActivityListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %d, want 2", len(logs))
	}
	var withDetails *store.ActivityLog
	for i := range logs {
		if logs[i].Action == "agent.update" {
			withDetails = &logs[i]
		}
	}
	if withDetails == nil {
		t.Fatal("row with details not returned")
	}
	if got := string(withDetails.Details); got != `{"field":"model"}` {
		t.Errorf("details = %q, want the seeded JSON", got)
	}
	if withDetails.CreatedAt.IsZero() {
		t.Error("created_at not parsed")
	}
}
