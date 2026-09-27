//go:build sqlite || sqliteonly

package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/sqlitestore"
)

// TestBackfillCapabilitiesOnSQLite pins the SQLite path: the statement is built
// from the store dialect, so it must not contain PostgreSQL-only functions
// (uuid_generate_v7, NOW) — the previous single-statement version failed every
// SQLite startup with "no such function: uuid_generate_v7".
func TestBackfillCapabilitiesOnSQLite(t *testing.T) {
	stores, err := sqlitestore.NewSQLiteStores(store.StoreConfig{
		SQLitePath:     filepath.Join(t.TempDir(), "goclaw.db"),
		StorageBackend: "sqlite",
	})
	if err != nil {
		t.Fatalf("NewSQLiteStores: %v", err)
	}
	defer stores.DB.Close()

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	agent := &store.AgentData{
		BaseModel:   store.BaseModel{ID: store.GenNewID()},
		TenantID:    store.MasterTenantID,
		AgentKey:    "backfill-agent",
		DisplayName: "Backfill Agent",
		AgentType:   store.AgentTypePredefined,
		Status:      "active",
		OwnerID:     "user-1",
	}
	if err := stores.Agents.Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	count, err := BackfillCapabilities(ctx, stores.DB, stores.Dialect)
	if err != nil {
		t.Fatalf("BackfillCapabilities: %v", err)
	}
	if count != 1 {
		t.Fatalf("backfilled %d agents, want 1", count)
	}

	files, err := stores.Agents.GetAgentContextFiles(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentContextFiles: %v", err)
	}
	var found *store.AgentContextFileData
	for i := range files {
		if files[i].FileName == CapabilitiesFile {
			found = &files[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("CAPABILITIES.md missing after backfill (%d files)", len(files))
	}
	if found.Content == "" {
		t.Error("backfilled CAPABILITIES.md is empty")
	}
	// The inserted row must carry the agent's tenant (isolation is per row).
	var rowTenant string
	if err := stores.DB.QueryRowContext(ctx,
		`SELECT tenant_id FROM agent_context_files WHERE agent_id = ? AND file_name = ?`,
		agent.ID, CapabilitiesFile).Scan(&rowTenant); err != nil {
		t.Fatalf("read backfilled tenant: %v", err)
	}
	if rowTenant != store.MasterTenantID.String() {
		t.Errorf("backfilled row tenant = %s, want %s", rowTenant, store.MasterTenantID)
	}

	// Idempotent: a second run must not duplicate the row.
	again, err := BackfillCapabilities(ctx, stores.DB, stores.Dialect)
	if err != nil {
		t.Fatalf("second BackfillCapabilities: %v", err)
	}
	if again != 0 {
		t.Fatalf("second run backfilled %d agents, want 0 (idempotent)", again)
	}

	// An agent that already has the file is left alone; a second agent without it
	// is picked up.
	second := &store.AgentData{
		BaseModel:   store.BaseModel{ID: store.GenNewID()},
		TenantID:    store.MasterTenantID,
		AgentKey:    "backfill-agent-2",
		DisplayName: "Backfill Agent 2",
		AgentType:   store.AgentTypePredefined,
		Status:      "active",
		OwnerID:     "user-1",
	}
	if err := stores.Agents.Create(ctx, second); err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	third, err := BackfillCapabilities(ctx, stores.DB, stores.Dialect)
	if err != nil {
		t.Fatalf("third BackfillCapabilities: %v", err)
	}
	if third != 1 {
		t.Fatalf("third run backfilled %d agents, want exactly the new one", third)
	}
	empty, err := stores.Agents.GetAgentContextFiles(ctx, uuid.Nil)
	if err != nil {
		t.Fatalf("read for an unknown agent must be a clean empty result, got %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("unknown agent returned %d context files", len(empty))
	}
}
