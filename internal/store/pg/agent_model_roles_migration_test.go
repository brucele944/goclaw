package pg

import (
	"database/sql"
	"net/url"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPGFreshDatabaseMigratesTo99AgentModelRoles proves the whole PG migration
// chain — including 000099_agent_model_roles — applies to a fresh database, that
// the down migration is reversible, and that the column reaches the agent store
// read path.
//
// It uses a throwaway database so the shared TEST_DATABASE_URL database (already
// migrated, used by sibling tests) is never rolled back. Skips when
// TEST_DATABASE_URL is unset or not a URL DSN.
func TestPGFreshDatabaseMigratesTo99AgentModelRoles(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping PG agent model_roles migration test")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("PG not reachable: %v", err)
	}
	defer admin.Close()

	tmpDB := "goclaw_mig_" + uuid.NewString()[:8]
	if _, err := admin.Exec("CREATE DATABASE " + tmpDB); err != nil {
		t.Fatalf("create temp database: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE IF EXISTS " + tmpDB + " WITH (FORCE)")
	})

	tmpDSN, ok := dsnWithDatabase(dsn, tmpDB)
	if !ok {
		t.Skipf("TEST_DATABASE_URL is not a URL DSN; cannot target a temp database")
	}

	m, err := migrate.New("file://../../../migrations", tmpDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up on fresh database: %v", err)
	}

	db, err := sql.Open("pgx", tmpDSN)
	if err != nil {
		t.Fatalf("open temp database: %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version < 99 {
		t.Fatalf("schema_migrations max = %d, want >= 99 (000099 must be applied)", version)
	}

	var dataType, nullable string
	var columnDefault *string
	if err := db.QueryRow(`SELECT data_type, is_nullable, column_default
		FROM information_schema.columns WHERE table_name='agents' AND column_name='model_roles'`).
		Scan(&dataType, &nullable, &columnDefault); err != nil {
		t.Fatalf("agents.model_roles missing after migration chain: %v", err)
	}
	if dataType != "jsonb" {
		t.Fatalf("agents.model_roles type = %q, want jsonb", dataType)
	}
	if nullable != "NO" {
		t.Fatalf("agents.model_roles nullable = %q, want NO", nullable)
	}
	if columnDefault == nil || *columnDefault != "'{}'::jsonb" {
		t.Fatalf("agents.model_roles default = %v, want '{}'::jsonb", columnDefault)
	}

	// A row inserted without model_roles (how a pre-000099 INSERT would look)
	// must read back as the empty object, and the store must scan it.
	tenantID := uuid.Must(uuid.NewV7())
	agentID := uuid.Must(uuid.NewV7())
	if _, err := db.Exec(
		`INSERT INTO tenants (id, name, slug, status) VALUES ($1,$2,$3,'active')`,
		tenantID, "mr-test-"+tenantID.String()[:8], "mr-"+tenantID.String()); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// display_name is supplied because scanAgentRow reads it as a plain string
	// (NULL would fail the scan).
	if _, err := db.Exec(
		`INSERT INTO agents (id, tenant_id, agent_key, display_name, agent_type, status, provider, model, owner_id)
		 VALUES ($1,$2,$3,'Model Roles Test','predefined','active','test','test-model','owner')`,
		agentID, tenantID, "mr-agent-"+agentID.String()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	var backfilled string
	if err := db.QueryRow("SELECT model_roles::text FROM agents WHERE id=$1", agentID).Scan(&backfilled); err != nil {
		t.Fatalf("read defaulted model_roles: %v", err)
	}
	if backfilled != "{}" {
		t.Fatalf("defaulted model_roles = %q, want {}", backfilled)
	}

	InitSqlx(db)
	agStore := &PGAgentStore{db: db}
	ag, err := agStore.GetByIDUnscoped(t.Context(), agentID)
	if err != nil {
		t.Fatalf("GetByIDUnscoped: %v", err)
	}
	if roles := ag.ParseModelRoles(); roles != nil {
		t.Fatalf("agent model roles = %+v, want nil for the '{}' default", roles)
	}

	if _, err := db.Exec(`UPDATE agents SET model_roles='{"coder":"openai/gpt-5"}'::jsonb WHERE id=$1`, agentID); err != nil {
		t.Fatalf("update model_roles: %v", err)
	}
	ag, err = agStore.GetByIDUnscoped(t.Context(), agentID)
	if err != nil {
		t.Fatalf("GetByIDUnscoped (after update): %v", err)
	}
	roles := ag.ParseModelRoles()
	if len(roles) != 1 || roles["coder"].Provider != "openai" || roles["coder"].Model != "gpt-5" {
		t.Fatalf("model roles read back = %+v, want coder=openai/gpt-5", roles)
	}

	// Round trip: migrate down to 98 (drops the column added by 000099), then back
	// up. Version-pinned rather than Steps(-1) so a later migration landing above
	// 000099 cannot make this assert the wrong step.
	if err := m.Migrate(98); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate down to 98: %v", err)
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name='agents' AND column_name='model_roles')`).Scan(&exists); err != nil {
		t.Fatalf("check column after down: %v", err)
	}
	if exists {
		t.Fatal("agents.model_roles still present after migrating 000099 down")
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up after down: %v", err)
	}
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name='agents' AND column_name='model_roles')`).Scan(&exists); err != nil {
		t.Fatalf("check column after up: %v", err)
	}
	if !exists {
		t.Fatal("agents.model_roles missing after re-applying 000099")
	}
}

// dsnWithDatabase rewrites the database name of a URL-form postgres DSN.
// Returns ok=false for keyword-form DSNs, which this test cannot retarget.
func dsnWithDatabase(dsn, database string) (string, bool) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.Path = "/" + database
	return u.String(), true
}
