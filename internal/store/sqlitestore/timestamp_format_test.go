//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// canonicalTimestamp matches the format the schema's DDL defaults write
// (strftime('%Y-%m-%dT%H:%M:%fZ','now')): UTC, millisecond precision.
var canonicalTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// TestSQLiteBoundTimestampsMatchDDLDefaults pins one text encoding for time
// columns. The driver writes a bound time.Time with time.Time.String()
// ("2026-09-27 06:13:53.08 +0000 UTC m=+8.334") unless it is normalised, while a
// column default writes strftime's "2026-09-27T06:13:53.080Z". SQLite compares
// TEXT lexically, so a bound row always sorted before a default-written row on
// the same day (' ' < 'T') and range filters compared two encodings.
func TestSQLiteBoundTimestampsMatchDDLDefaults(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	tenant := store.MasterTenantID.String()

	// defaultRow relies on the column default; boundRow binds a time.Time.
	defaultRow := uuid.New().String()
	boundRow := uuid.New().String()
	insert := func(id, action string, at any) {
		t.Helper()
		var err error
		if at == nil {
			_, err = db.ExecContext(ctx,
				`INSERT INTO activity_logs (id, actor_type, actor_id, action, tenant_id)
				 VALUES (?, 'user', 'user-1', ?, ?)`, id, action, tenant)
		} else {
			_, err = db.ExecContext(ctx,
				`INSERT INTO activity_logs (id, actor_type, actor_id, action, tenant_id, created_at)
				 VALUES (?, 'user', 'user-1', ?, ?, ?)`, id, action, tenant, at)
		}
		if err != nil {
			t.Fatalf("insert %s: %v", action, err)
		}
	}
	insert(defaultRow, "default", nil)
	// Bound strictly later than the default-written row above: before the fix this
	// row still sorted first because its text encoding started with a space.
	insert(boundRow, "bound", time.Now().UTC().Add(time.Minute))

	rows, err := db.QueryContext(ctx, `SELECT id, created_at FROM activity_logs ORDER BY created_at`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var order []string
	var seen []string
	for rows.Next() {
		var id, createdAt string
		if err := rows.Scan(&id, &createdAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		order = append(order, id)
		seen = append(seen, createdAt)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("rows = %d, want 2", len(order))
	}
	for _, createdAt := range seen {
		if !canonicalTimestamp.MatchString(createdAt) {
			t.Errorf("created_at = %q, want the DDL default encoding %q", createdAt, sqliteTimestampLayout)
		}
	}
	if order[0] != defaultRow || order[1] != boundRow {
		t.Errorf("ORDER BY created_at = %v, want [default bound] (bound row is later)", order)
	}

	// A range filter must compare the two rows' encodings correctly. The bound is
	// strictly between them: the default row is written at ~now, the bound row a
	// minute later. (Both encodings carry millisecond precision, so a bound of
	// "now" can still match the default row written in the same millisecond.)
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_logs WHERE created_at >= ?`, time.Now().UTC().Add(30*time.Second),
	).Scan(&count); err != nil {
		t.Fatalf("range filter: %v", err)
	}
	if count != 1 {
		t.Errorf("rows newer than now = %d, want 1 (the bound row)", count)
	}
}

// TestSQLiteNullableTimestampMatchesDDLDefault exercises the *time.Time shape
// AGENTS.md mandates for nullable timestamp columns (e.g. api_keys.expires_at).
// A bare `v.(time.Time)` type assertion in CheckNamedValue misses this entirely:
// database/sql's DefaultParameterConverter dereferences the pointer to a plain
// time.Time before the driver ever sees it, so a checker that only matches
// time.Time directly never runs for *time.Time, sql.NullTime, or any other
// driver.Valuer — those still land in the old time.Time.String() encoding.
func TestSQLiteNullableTimestampMatchesDDLDefault(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	ctx := context.Background()
	apiKeys := NewSQLiteAPIKeyStore(db)

	expiresLaterToday := time.Now().UTC().Add(2 * time.Hour)
	key := &store.APIKeyData{
		ID: uuid.New(), Name: "expiring-key", Prefix: "sk_test",
		KeyHash: "hash-" + uuid.New().String(), ExpiresAt: &expiresLaterToday,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := apiKeys.Create(ctx, key); err != nil {
		t.Fatalf("create: %v", err)
	}

	var raw string
	if err := db.QueryRowContext(ctx, `SELECT expires_at FROM api_keys WHERE id = ?`, key.ID).Scan(&raw); err != nil {
		t.Fatalf("select expires_at: %v", err)
	}
	if !canonicalTimestamp.MatchString(raw) {
		t.Errorf("expires_at = %q (bound via *time.Time), want the DDL default encoding %q", raw, sqliteTimestampLayout)
	}

	// The production query api_keys.go:94 compares expires_at against the same
	// strftime encoding. A key expiring later today must still be found: before
	// the ConvertValue fix, expires_at was ' '-separated and sorted before the
	// 'T'-separated comparison value regardless of the actual expiry time.
	got, err := apiKeys.GetByHash(ctx, key.KeyHash)
	if err != nil {
		t.Fatalf("GetByHash (not-yet-expired key): %v", err)
	}
	if got == nil || got.ID != key.ID {
		t.Fatalf("GetByHash returned %+v, want key %s", got, key.ID)
	}
}
