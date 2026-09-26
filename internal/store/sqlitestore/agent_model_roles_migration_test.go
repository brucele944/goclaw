//go:build sqlite || sqliteonly

package sqlitestore

import (
	"testing"

	"github.com/google/uuid"
)

// TestSQLiteSchemaUpgrade_61_to_62_AgentModelRoles verifies the v61 → v62
// migration mirrors migrations/000099_agent_model_roles.up.sql: agents.model_roles
// exists, defaults to an empty object for pre-existing rows, and the recorded
// schema version advances.
func TestSQLiteSchemaUpgrade_61_to_62_AgentModelRoles(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// openTestDB applies the LATEST schema; strip the v62 addition so EnsureSchema
	// exercises the real v61 → v62 patch, then reset the recorded version.
	mustExec(t, db, `ALTER TABLE agents DROP COLUMN model_roles`)
	mustExec(t, db, `UPDATE schema_version SET version = 61`)

	tenantID := uuid.Must(uuid.NewV7())
	agentID := uuid.Must(uuid.NewV7())
	mustExec(t, db, `INSERT INTO tenants (id, name, slug, status) VALUES (?,?,?,'active')`,
		tenantID, "model-roles-"+tenantID.String()[:8], "mr-"+tenantID.String())
	mustExec(t, db, `INSERT INTO agents (id, tenant_id, agent_key, agent_type, status, provider, model, owner_id)
		VALUES (?,?,?,'predefined','active','test','test-model','owner')`,
		agentID, tenantID, "mr-agent-"+agentID.String())
	t.Cleanup(func() {
		db.Exec("DELETE FROM agents WHERE id=?", agentID)
		db.Exec("DELETE FROM tenants WHERE id=?", tenantID)
	})

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema (v61→62) failed: %v", err)
	}

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if SchemaVersion < 62 {
		t.Fatalf("SchemaVersion = %d, want >= 62", SchemaVersion)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}

	if !columnExists(t, db, "agents", "model_roles") {
		t.Fatal("agents.model_roles missing after v61 → v62")
	}

	// The pre-existing row must carry the NOT NULL default of an empty object.
	var preExisting string
	if err := db.QueryRow("SELECT model_roles FROM agents WHERE id=?", agentID).Scan(&preExisting); err != nil {
		t.Fatalf("read backfilled model_roles: %v", err)
	}
	if preExisting != "{}" {
		t.Fatalf("backfilled model_roles = %q, want %q", preExisting, "{}")
	}

	// And the column must round-trip a real role map.
	raw := `{"coder":"openai/gpt-5"}`
	if _, err := db.Exec("UPDATE agents SET model_roles=? WHERE id=?", raw, agentID); err != nil {
		t.Fatalf("write model_roles: %v", err)
	}
	if err := db.QueryRow("SELECT model_roles FROM agents WHERE id=?", agentID).Scan(&preExisting); err != nil {
		t.Fatalf("read model_roles: %v", err)
	}
	if preExisting != raw {
		t.Fatalf("model_roles = %q, want %q", preExisting, raw)
	}
}
