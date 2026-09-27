package bootstrap

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/base"
)

// BackfillCapabilities seeds CAPABILITIES.md for every agent that does not have
// it yet. Runs once at startup, idempotent.
//
// The work is done in the caller's dialect: a PostgreSQL database can generate
// the row id and the timestamps in SQL, while SQLite has neither
// uuid_generate_v7() nor NOW(), so ids come from Go and timestamps are bound as
// values. Both paths insert the same rows; only the expressions differ.
func BackfillCapabilities(ctx context.Context, db *sql.DB, dialect base.Dialect) (int64, error) {
	if db == nil {
		return 0, nil
	}
	if dialect == nil {
		// No dialect to build SQL with: better to skip than to emit statements the
		// backend may reject.
		return 0, nil
	}

	tpl, err := templateFS.ReadFile(templatePath(CapabilitiesFile))
	if err != nil {
		return 0, err
	}

	// Agents missing the file, with the tenant each row belongs to (tenant_id is
	// carried onto the inserted row to preserve isolation).
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, a.tenant_id
		FROM agents a
		WHERE a.tenant_id IS NOT NULL
		  AND NOT EXISTS (
			SELECT 1 FROM agent_context_files acf
			WHERE acf.agent_id = a.id AND acf.file_name = 'CAPABILITIES.md'
		)`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		agentID  any
		tenantID any
	}
	var targets []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.agentID, &p.tenantID); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(targets) == 0 {
		return 0, nil
	}

	ph := func(n int) string { return dialect.Placeholder(n) }
	insert := "INSERT INTO agent_context_files " +
		"(id, agent_id, file_name, content, created_at, updated_at, tenant_id) VALUES (" +
		ph(1) + ", " + ph(2) + ", 'CAPABILITIES.md', " + ph(3) + ", " + ph(4) + ", " + ph(5) + ", " + ph(6) + ")"

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	var count int64
	for _, p := range targets {
		// file_name is a constant and only content is parameterized, so the
		// statement stays a single prepared form for every row.
		if _, err := stmt.ExecContext(ctx, store.GenNewID(), p.agentID, string(tpl), now, now, p.tenantID); err != nil {
			_ = tx.Rollback()
			return count, err
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if count > 0 {
		slog.Info("bootstrap: backfilled CAPABILITIES.md", "agents", count)
	}
	return count, nil
}
