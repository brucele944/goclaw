//go:build sqlite || sqliteonly

package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
	"github.com/nextlevelbuilder/goclaw/internal/store/sqlitestore"
)

// newImportTestDB returns an in-memory SQLite database with the full schema, a
// tenant-scoped context, one agent to import into, and a handler bound to both.
func newImportTestDB(t *testing.T) (context.Context, *sql.DB, *AgentsHandler, *store.AgentData) {
	t.Helper()
	db, err := sqlitestore.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	agents := sqlitestore.NewSQLiteAgentStore(db)
	ag := &store.AgentData{
		AgentKey:    "import-target",
		DisplayName: "Import Target",
		AgentType:   "open",
		Status:      "active",
	}
	if err := agents.Create(ctx, ag); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	// Extra agents so team member batches carry more than one row: the lead is
	// inserted on its own, and agent_team_members is keyed by (team_id, agent_id),
	// so repeated keys would be deduplicated instead of exercising the batch.
	for _, key := range []string{"import-peer-a", "import-peer-b"} {
		peer := &store.AgentData{
			AgentKey:    key,
			DisplayName: key,
			AgentType:   "open",
			Status:      "active",
		}
		if err := agents.Create(ctx, peer); err != nil {
			t.Fatalf("create agent %s: %v", key, err)
		}
	}
	return ctx, db, &AgentsHandler{db: db, agents: agents}, ag
}

func rowCount(t *testing.T, ctx context.Context, db *sql.DB, table string) int {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return got
}

// TestImportSectionsPersistRowsOnSQLite pins the SQLite contract of the shared
// agent-import SQL. The statements run on both dialects, so they must not rely on
// PostgreSQL-only DDL defaults (uuid_generate_v7() for `id` columns) and must not
// bind NULL into NOT NULL columns (metadata, task_type, task_number). Either
// mistake aborts the statement, and the import only logs a warning — the section
// then reports success while the rows silently never land.
func TestImportSectionsPersistRowsOnSQLite(t *testing.T) {
	ctx, db, h, ag := newImportTestDB(t)

	createdAt := time.Now().UTC().Format(time.RFC3339)
	intervalMS := int64(3600000)
	provider, model, timezone := "openai", "test-model", "UTC"
	strPtr := func(s string) *string { return &s }
	intPtr := func(i int) *int { return &i }
	arc := &importArchive{
		cronJobs: []pg.CronJobExport{{
			Name: "imported-job", ScheduleKind: "every", IntervalMS: &intervalMS,
			Timezone: &timezone, Payload: json.RawMessage(`{"prompt":"hi"}`),
		}},
		userOverrides: []pg.UserOverrideExport{{
			UserID: "user-1", Provider: &provider, Model: &model,
			Settings: json.RawMessage(`{"temperature":0.2}`),
		}},
		evolutionMetrics: []pg.EvolutionMetricExport{{
			SessionKey: "session-1", MetricType: "tokens", MetricKey: "total",
			Value: json.RawMessage(`{"n":1}`), CreatedAt: createdAt,
		}},
		evolutionSuggestions: []pg.EvolutionSuggestionExport{{
			SuggestionType: "prompt", Suggestion: "tighten the system prompt",
			Rationale: "repeated retries", Status: "pending", CreatedAt: createdAt,
		}},
		teamMeta: &pg.TeamExport{Name: "Imported Team", Status: "active"},
		// Two rows per child table: a wrong placeholder stride only corrupts rows
		// after the first, so a single-row fixture cannot see it.
		teamMembers: []pg.TeamMemberExport{
			{AgentKey: "import-peer-a", Role: "member"},
			{AgentKey: "import-peer-b", Role: "observer"},
		},
		teamTasks: []pg.TeamTaskExport{
			{Subject: "T1", Status: "pending", Priority: 1},
			{Subject: "T2", Status: "pending", Priority: 2, TaskType: strPtr("general"), TaskNumber: intPtr(2)},
		},
		teamComments: []pg.TeamTaskCommentExport{
			{TaskIdx: 0, Content: "first comment", CommentType: "note"},
			{TaskIdx: 1, Content: "second comment", CommentType: "review"},
		},
		teamEvents: []pg.TeamTaskEventExport{
			{TaskIdx: 0, EventType: "created", ActorType: "user", ActorID: "user-1"},
			{TaskIdx: 1, EventType: "assigned", ActorType: "agent", ActorID: "import-target"},
		},
	}

	summary := &ImportSummary{}
	h.importCron(ctx, ag, arc, summary, nil)
	h.importUserOverrides(ctx, ag, arc, summary, nil)
	h.importEvolution(ctx, ag, arc, summary, nil)
	if err := h.importTeamSection(ctx, ag, arc, nil); err != nil {
		t.Fatalf("import team section: %v", err)
	}

	for _, tc := range []struct {
		table string
		want  int
	}{
		{"cron_jobs", 1},
		{"user_agent_overrides", 1},
		{"agent_evolution_metrics", 1},
		{"agent_evolution_suggestions", 1},
		{"agent_teams", 1},
		{"agent_team_members", 3}, // lead + two imported peers
		{"team_tasks", 2},
		{"team_task_comments", 2},
		{"team_task_events", 2},
	} {
		if got := rowCount(t, ctx, db, tc.table); got != tc.want {
			t.Errorf("%s rows = %d, want %d (statement failed silently)", tc.table, got, tc.want)
		}
	}

	// Values, not just counts: a wrong placeholder stride binds a neighbouring
	// row's parameter, which keeps the row count intact but corrupts the data.
	for _, tc := range []struct {
		table  string
		column string
		want   []string
	}{
		{"agent_team_members", "role", []string{"lead", "member", "observer"}},
		{"team_tasks", "subject", []string{"T1", "T2"}},
		{"team_task_comments", "content", []string{"first comment", "second comment"}},
		{"team_task_events", "actor_id", []string{"import-target", "user-1"}},
	} {
		rows, err := db.QueryContext(ctx,
			fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", tc.column, tc.table, tc.column))
		if err != nil {
			t.Fatalf("select %s.%s: %v", tc.table, tc.column, err)
		}
		var got []string
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan %s.%s: %v", tc.table, tc.column, err)
			}
			got = append(got, v)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s: %v", tc.table, err)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s.%s rows = %d, want %d", tc.table, tc.column, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s.%s[%d] = %q, want %q (placeholder stride drift)",
					tc.table, tc.column, i, got[i], tc.want[i])
			}
		}
	}

	if summary.CronJobs != 1 || summary.UserOverrides != 1 || summary.EvolutionMetrics != 1 || summary.EvolutionSuggestions != 1 {
		t.Errorf("summary = cron:%d overrides:%d metrics:%d suggestions:%d, want 1 each",
			summary.CronJobs, summary.UserOverrides, summary.EvolutionMetrics, summary.EvolutionSuggestions)
	}
}

// TestImportCronRespectsSQLiteBindLimit imports one row past the point where a
// single statement can bind every row. SQLite allows 32766 parameters (32767 fails
// with "too many SQL variables") and a cron row binds 11 of them, so a fixed
// 5000-row chunk would emit one 55000-parameter statement and lose every row. The
// row count here is the minimum that crosses the limit: 2979 > 32766/11.
func TestImportCronRespectsSQLiteBindLimit(t *testing.T) {
	ctx, db, h, ag := newImportTestDB(t)

	const cronRows = 2979

	intervalMS := int64(1000)
	timezone := "UTC"
	arc := &importArchive{}
	for i := range cronRows {
		arc.cronJobs = append(arc.cronJobs, pg.CronJobExport{
			Name: fmt.Sprintf("job-%d", i), ScheduleKind: "every",
			IntervalMS: &intervalMS, Timezone: &timezone,
			Payload: json.RawMessage(`{"prompt":"hi"}`),
		})
	}

	summary := &ImportSummary{}
	h.importCron(ctx, ag, arc, summary, nil)

	if summary.CronJobs != cronRows {
		t.Errorf("summary.CronJobs = %d, want %d (counter must count only successful batches)", summary.CronJobs, cronRows)
	}
	if got := rowCount(t, ctx, db, "cron_jobs"); got != cronRows {
		t.Errorf("cron_jobs rows = %d, want %d (batch exceeded the bind limit)", got, cronRows)
	}
}

// TestBindLimitedChunkSize pins the batch sizing contract every import section
// relies on: a full chunk must stay inside the bound-parameter ceiling of both
// dialects, while still packing as many rows as the ceiling allows.
func TestBindLimitedChunkSize(t *testing.T) {
	for _, tc := range []struct {
		name         string
		paramsPerRow int
		extraParams  int
	}{
		{"cron", 11, 0},
		{"overrides", 7, 1}, // +1 for the batch-level updated_at
		{"comments", 9, 0},
		{"events", 8, 0},
		{"links", 7, 0},
		{"members", 5, 0},
		{"profiles", 3, 0},
	} {
		n := bindLimitedChunkSize(tc.paramsPerRow, tc.extraParams)
		if n < 1 {
			t.Errorf("%s: chunk size = %d, want >= 1", tc.name, n)
			continue
		}
		if bound := n*tc.paramsPerRow + tc.extraParams; bound > maxBindVars {
			t.Errorf("%s: full chunk binds %d parameters, ceiling is %d", tc.name, bound, maxBindVars)
		}
		if bound := (n+1)*tc.paramsPerRow + tc.extraParams; bound <= maxBindVars {
			t.Errorf("%s: chunk size %d leaves room for another row (%d <= %d)", tc.name, n, bound, maxBindVars)
		}
	}
	// Degenerate input must not produce a zero-size chunk (an infinite loop).
	if n := bindLimitedChunkSize(0, 0); n < 1 {
		t.Errorf("bindLimitedChunkSize(0, 0) = %d, want >= 1", n)
	}
	if n := bindLimitedChunkSize(maxBindVars+1, 0); n < 1 {
		t.Errorf("bindLimitedChunkSize over the ceiling = %d, want >= 1", n)
	}
}
