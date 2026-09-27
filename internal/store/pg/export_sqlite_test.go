//go:build sqlite || sqliteonly

package pg

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/sqlitestore"
)

// The export helpers take a *sql.DB and run on both backends: the gateway calls
// them with whatever handle the active store exposes. On the SQLite/lite build
// pkgSqlxDB is never initialized (initSqlx runs from the PostgreSQL factory only)
// and timestamp/array columns come back as the TEXT the driver stored, not as
// time.Time and text[] literals. Both are pinned here — the helpers used to
// dereference the nil package handle and, once reachable, would have failed to
// scan the SQLite representations.
func newExportTestDB(t *testing.T) (context.Context, *sql.DB, *store.AgentData) {
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
	ag := &store.AgentData{AgentKey: "export-source", DisplayName: "Export Source", AgentType: "open", Status: "active"}
	if err := agents.Create(ctx, ag); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return ctx, db, ag
}

func execSeed(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("seed %q: %v", query[:40], err)
	}
}

// exportedAt is the timestamp seeded into every table under test; the helpers
// must hand it back in the archive's RFC3339 UTC seconds form.
const (
	seededAt     = "2026-01-02T03:04:05.000Z"
	normalizedAt = "2026-01-02T03:04:05Z"
)

func TestExportSkillsAndGrantsOnSQLite(t *testing.T) {
	ctx, db, ag := newExportTestDB(t)
	tenant := store.MasterTenantID.String()
	skillID := uuid.New().String()

	execSeed(t, db, `INSERT INTO skills (id, name, slug, description, owner_id, visibility, version,
		status, frontmatter, file_path, tags, is_system, deps, enabled, tenant_id)
		VALUES (?, 'audit-skill', 'audit-skill', 'desc', 'owner', 'private', 3, 'active',
		'{"author":"tester"}', '/tmp/audit-skill', '["alpha","beta"]', 0, '{"bins":[]}', 1, ?)`,
		skillID, tenant)
	execSeed(t, db, `INSERT INTO skill_agent_grants (id, skill_id, agent_id, pinned_version, granted_by, tenant_id)
		VALUES (?, ?, ?, 3, 'smoke-user', ?)`, uuid.New().String(), skillID, ag.ID.String(), tenant)

	skills, err := ExportSkills(ctx, db, SkillExportSelection{})
	if err != nil {
		t.Fatalf("ExportSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want 1", len(skills))
	}
	if got := skills[0].Tags; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("tags = %v, want [alpha beta]", got)
	}
	if skills[0].Name != "audit-skill" || skills[0].Version != 3 {
		t.Errorf("skill = %+v", skills[0])
	}

	selected, err := ExportSkills(ctx, db, SkillExportSelection{IDs: []uuid.UUID{uuid.MustParse(skillID)}})
	if err != nil {
		t.Fatalf("ExportSkills by id: %v", err)
	}
	if len(selected) != 1 || selected[0].ID != skillID {
		t.Errorf("selected = %+v, want the seeded skill", selected)
	}

	grants, err := ExportSkillGrantsWithAgentKey(ctx, db, uuid.MustParse(skillID))
	if err != nil {
		t.Fatalf("ExportSkillGrantsWithAgentKey: %v", err)
	}
	if len(grants) != 1 || grants[0].AgentKey != ag.AgentKey || grants[0].PinnedVersion != 3 {
		t.Errorf("grants = %+v, want agent_key %q pinned 3", grants, ag.AgentKey)
	}
}

func TestExportMemoryAndKnowledgeOnSQLite(t *testing.T) {
	ctx, db, ag := newExportTestDB(t)
	tenant := store.MasterTenantID.String()
	agentID := ag.ID.String()

	execSeed(t, db, `INSERT INTO episodic_summaries (id, tenant_id, agent_id, user_id, session_key,
		summary, l0_abstract, key_topics, turn_count, token_count, created_at, expires_at)
		VALUES (?, ?, ?, 'u1', 'sess-1', 'summary text', 'abstract', '["k1","k2"]', 4, 40, ?, NULL)`,
		uuid.New().String(), tenant, agentID, seededAt)

	summaries, err := ExportEpisodicSummaries(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportEpisodicSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(summaries))
	}
	if got := summaries[0].KeyTopics; len(got) != 2 || got[0] != "k1" || got[1] != "k2" {
		t.Errorf("key_topics = %v, want [k1 k2]", got)
	}
	if summaries[0].CreatedAt != normalizedAt {
		t.Errorf("created_at = %q, want %q", summaries[0].CreatedAt, normalizedAt)
	}
	if summaries[0].ExpiresAt != nil {
		t.Errorf("expires_at = %v, want nil", *summaries[0].ExpiresAt)
	}

	entityID := uuid.New().String()
	execSeed(t, db, `INSERT INTO kg_entities (id, agent_id, user_id, external_id, name, entity_type,
		properties, source_id, confidence, tenant_id, created_at, updated_at)
		VALUES (?, ?, 'u1', 'ext-1', 'Alice', 'Person', '{"role":"dev"}', 'src', 0.9, ?, ?, ?)`,
		entityID, agentID, tenant, seededAt, seededAt)
	targetID := uuid.New().String()
	execSeed(t, db, `INSERT INTO kg_entities (id, agent_id, user_id, external_id, name, entity_type,
		properties, source_id, confidence, tenant_id, created_at, updated_at)
		VALUES (?, ?, 'u1', 'ext-2', 'Bob', 'Person', '{}', 'src', 0.8, ?, ?, ?)`,
		targetID, agentID, tenant, seededAt, seededAt)
	execSeed(t, db, `INSERT INTO kg_relations (id, agent_id, user_id, source_entity_id, relation_type,
		target_entity_id, confidence, properties, tenant_id, created_at)
		VALUES (?, ?, 'u1', ?, 'KNOWS', ?, 0.7, '{}', ?, ?)`,
		uuid.New().String(), agentID, entityID, targetID, tenant, seededAt)

	entities, err := ExportKGEntities(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportKGEntities: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(entities))
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	for _, e := range entities {
		if e.CreatedAt != want || e.UpdatedAt != want {
			t.Errorf("entity %s timestamps = %d/%d, want %d", e.Name, e.CreatedAt, e.UpdatedAt, want)
		}
	}
	// Entities come back in cursor (id) order, which is not insertion order.
	var alice *store.Entity
	for i := range entities {
		if entities[i].Name == "Alice" {
			alice = &entities[i]
		}
	}
	if alice == nil || alice.Properties["role"] != "dev" {
		t.Errorf("Alice properties = %v", alice)
	}

	relations, err := ExportKGRelations(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportKGRelations: %v", err)
	}
	if len(relations) != 1 || relations[0].CreatedAt != want {
		t.Errorf("relations = %+v, want one with created_at %d", relations, want)
	}
}

func TestExportSchedulesAndVaultOnSQLite(t *testing.T) {
	ctx, db, ag := newExportTestDB(t)
	tenant := store.MasterTenantID.String()
	agentID := ag.ID.String()

	execSeed(t, db, `INSERT INTO cron_jobs (id, agent_id, name, schedule_kind, run_at, timezone,
		payload, delete_after_run, tenant_id)
		VALUES (?, ?, 'audit-job', 'at', ?, 'UTC', '{"prompt":"hi"}', 0, ?)`,
		uuid.New().String(), agentID, seededAt, tenant)

	jobs, err := ExportCronJobs(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportCronJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].RunAt == nil || *jobs[0].RunAt != normalizedAt {
		t.Errorf("run_at = %v, want %q", jobs[0].RunAt, normalizedAt)
	}
	if string(jobs[0].Payload) != `{"prompt":"hi"}` {
		t.Errorf("payload = %s", jobs[0].Payload)
	}

	docID, doc2ID := uuid.New().String(), uuid.New().String()
	for i, id := range []string{docID, doc2ID} {
		execSeed(t, db, `INSERT INTO vault_documents (id, tenant_id, agent_id, scope, path, title,
			doc_type, content_hash, summary, metadata, created_at, updated_at)
			VALUES (?, ?, ?, 'personal', ?, ?, 'note', 'hash', 'sum', '{}', ?, ?)`,
			id, tenant, agentID, "/docs/doc"+string(rune('1'+i))+".md", "Doc", seededAt, seededAt)
	}
	execSeed(t, db, `INSERT INTO vault_links (id, from_doc_id, to_doc_id, link_type, context, created_at)
		VALUES (?, ?, ?, 'wikilink', 'ctx', ?)`, uuid.New().String(), docID, doc2ID, seededAt)

	docs, err := ExportVaultDocuments(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportVaultDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("documents = %d, want 2", len(docs))
	}
	for _, d := range docs {
		if d.CreatedAt != normalizedAt || d.UpdatedAt != normalizedAt {
			t.Errorf("document %s timestamps = %q/%q, want %q", d.Path, d.CreatedAt, d.UpdatedAt, normalizedAt)
		}
	}

	links, err := ExportVaultLinks(ctx, db, ag.ID)
	if err != nil {
		t.Fatalf("ExportVaultLinks: %v", err)
	}
	if len(links) != 1 || links[0].CreatedAt != normalizedAt {
		t.Errorf("links = %+v, want one with created_at %q", links, normalizedAt)
	}
}

func TestNormalizeArchiveTime(t *testing.T) {
	cases := map[string]string{
		seededAt:                           normalizedAt,
		"2026-01-02T03:04:05Z":             normalizedAt,
		"2026-01-02T10:04:05+07:00":        normalizedAt,
		"2026-01-02 03:04:05.123456+00:00": normalizedAt,
		"2026-01-02 03:04:05":              normalizedAt,
		"":                                 "",
		"not-a-time":                       "not-a-time",
	}
	for in, want := range cases {
		if got := normalizeArchiveTime(in); got != want {
			t.Errorf("normalizeArchiveTime(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEveryExportFunctionOnSQLite calls the whole shared export surface against a
// SQLite database seeded with one row per table it reads. The helpers run on both
// dialects, so each one must decode what the SQLite driver returns: TEXT
// timestamps, JSON arrays, JSON columns as strings, and NULL in nullable columns.
func TestEveryExportFunctionOnSQLite(t *testing.T) {
	ctx, db, ag := newExportTestDB(t)
	tenant := store.MasterTenantID.String()
	agentID := ag.ID.String()
	ctx = store.WithUserID(ctx, "u1")

	// --- context files, memory, config, profiles, overrides -------------------
	execSeed(t, db, `INSERT INTO agent_context_files (id, agent_id, file_name, content, tenant_id)
		VALUES (?, ?, 'SOUL.md', 'soul', ?)`, uuid.New().String(), agentID, tenant)
	execSeed(t, db, `INSERT INTO user_context_files (id, agent_id, user_id, file_name, content, tenant_id)
		VALUES (?, ?, 'u1', 'USER.md', 'user', ?)`, uuid.New().String(), agentID, tenant)
	// user_id is nullable: a NULL here must not abort the row (or the section).
	execSeed(t, db, `INSERT INTO memory_documents (id, agent_id, user_id, path, content, hash, tenant_id)
		VALUES (?, ?, NULL, 'notes.md', 'body', 'hash', ?)`, uuid.New().String(), agentID, tenant)
	execSeed(t, db, `INSERT INTO agent_config_permissions (id, agent_id, scope, config_type, user_id,
		permission, metadata, tenant_id) VALUES (?, ?, 'agent', 'mcp', 'u1', 'allow', '{"src":"ui"}', ?)`,
		uuid.New().String(), agentID, tenant)
	execSeed(t, db, `INSERT INTO user_agent_profiles (agent_id, user_id, workspace, tenant_id)
		VALUES (?, 'u1', '/ws/u1', ?)`, agentID, tenant)
	execSeed(t, db, `INSERT INTO user_agent_overrides (id, agent_id, user_id, provider, model, settings, tenant_id)
		VALUES (?, ?, 'u1', 'openai', 'mock-model', '{"temperature":0.2}', ?)`,
		uuid.New().String(), agentID, tenant)

	// --- skills, schedules, memory tiers (also feed the preview counts) --------
	skillID := uuid.New().String()
	execSeed(t, db, `INSERT INTO skills (id, name, slug, owner_id, visibility, version, status,
		frontmatter, file_path, tags, is_system, deps, enabled, tenant_id)
		VALUES (?, 'audit-skill', 'audit-skill', 'owner', 'private', 1, 'active',
		'{}', '/tmp/audit-skill', '["alpha"]', 0, '{}', 1, ?)`, skillID, tenant)
	execSeed(t, db, `INSERT INTO skill_agent_grants (id, skill_id, agent_id, pinned_version, granted_by, tenant_id)
		VALUES (?, ?, ?, 1, 'smoke-user', ?)`, uuid.New().String(), skillID, agentID, tenant)
	execSeed(t, db, `INSERT INTO cron_jobs (id, agent_id, name, schedule_kind, run_at, payload, tenant_id)
		VALUES (?, ?, 'audit-job', 'at', ?, '{"prompt":"hi"}', ?)`,
		uuid.New().String(), agentID, seededAt, tenant)
	execSeed(t, db, `INSERT INTO episodic_summaries (id, tenant_id, agent_id, session_key, summary,
		key_topics, created_at, expires_at) VALUES (?, ?, ?, 'sess-1', 'summary', '["k1"]', ?, NULL)`,
		uuid.New().String(), tenant, agentID, seededAt)
	// NULL source_id: nullable in the DDL, so the export must not abort the row.
	execSeed(t, db, `INSERT INTO kg_entities (id, agent_id, user_id, external_id, name, entity_type,
		properties, source_id, confidence, tenant_id, created_at, updated_at)
		VALUES (?, ?, 'u1', 'ext-null', 'Carol', 'Person', '{}', NULL, 0.5, ?, ?, ?)`,
		uuid.New().String(), agentID, tenant, seededAt, seededAt)
	docID, doc2ID := uuid.New().String(), uuid.New().String()
	for i, id := range []string{docID, doc2ID} {
		execSeed(t, db, `INSERT INTO vault_documents (id, tenant_id, agent_id, scope, path, title,
			metadata, created_at, updated_at) VALUES (?, ?, ?, 'personal', ?, 'Doc', '{}', ?, ?)`,
			id, tenant, agentID, "/docs/audit"+string(rune('1'+i))+".md", seededAt, seededAt)
	}
	execSeed(t, db, `INSERT INTO vault_links (id, from_doc_id, to_doc_id, created_at)
		VALUES (?, ?, ?, ?)`, uuid.New().String(), docID, doc2ID, seededAt)

	// --- mcp grants -----------------------------------------------------------
	serverID := uuid.New().String()
	execSeed(t, db, `INSERT INTO mcp_servers (id, name, transport, created_by, tenant_id)
		VALUES (?, 'audit-server', 'stdio', 'smoke-user', ?)`, serverID, tenant)
	execSeed(t, db, `INSERT INTO mcp_agent_grants (id, server_id, agent_id, enabled, tool_allow,
		tool_deny, config_overrides, granted_by, tenant_id)
		VALUES (?, ?, ?, 1, '["a"]', NULL, '{"timeout":5}', 'smoke-user', ?)`,
		uuid.New().String(), serverID, agentID, tenant)

	// --- evolution ------------------------------------------------------------
	execSeed(t, db, `INSERT INTO agent_evolution_metrics (id, tenant_id, agent_id, session_key,
		metric_type, metric_key, value) VALUES (?, ?, ?, 'sess-1', 'tokens', 'total', '{"n":7}')`,
		uuid.New().String(), tenant, agentID)
	execSeed(t, db, `INSERT INTO agent_evolution_suggestions (id, tenant_id, agent_id, suggestion_type,
		suggestion, rationale, parameters, status, reviewed_at)
		VALUES (?, ?, ?, 'prompt', 'tighten it', 'repeated retries', '{"k":"v"}', 'pending', NULL)`,
		uuid.New().String(), tenant, agentID)

	// --- team -----------------------------------------------------------------
	teamID := uuid.New().String()
	execSeed(t, db, `INSERT INTO agent_teams (id, name, lead_agent_id, status, settings, created_by, tenant_id)
		VALUES (?, 'Audit Team', ?, 'active', '{"k":1}', 'smoke-user', ?)`, teamID, agentID, tenant)
	execSeed(t, db, `INSERT INTO agent_team_members (team_id, agent_id, role, tenant_id)
		VALUES (?, ?, 'lead', ?)`, teamID, agentID, tenant)
	taskID := uuid.New().String()
	execSeed(t, db, `INSERT INTO team_tasks (id, team_id, subject, status, metadata, tenant_id)
		VALUES (?, ?, 'Audit task', 'pending', '{"m":1}', ?)`, taskID, teamID, tenant)
	execSeed(t, db, `INSERT INTO team_task_comments (id, task_id, user_id, content, metadata,
		comment_type, tenant_id) VALUES (?, ?, 'u1', 'a comment', '{"c":1}', 'note', ?)`,
		uuid.New().String(), taskID, tenant)
	execSeed(t, db, `INSERT INTO team_task_events (id, task_id, event_type, actor_type, actor_id, data, tenant_id)
		VALUES (?, ?, 'created', 'user', 'u1', '{"e":1}', ?)`, uuid.New().String(), taskID, tenant)
	peer := &store.AgentData{AgentKey: "export-peer", DisplayName: "Peer", AgentType: "open", Status: "active"}
	if err := sqlitestore.NewSQLiteAgentStore(db).Create(ctx, peer); err != nil {
		t.Fatalf("create peer agent: %v", err)
	}
	// ExportTeamByLead reports the non-lead members, so the team needs one.
	execSeed(t, db, `INSERT INTO agent_team_members (team_id, agent_id, role, tenant_id)
		VALUES (?, ?, 'member', ?)`, teamID, peer.ID.String(), tenant)
	execSeed(t, db, `INSERT INTO agent_links (id, source_agent_id, target_agent_id, direction,
		description, created_by, tenant_id) VALUES (?, ?, ?, 'outbound', 'peer link', 'smoke-user', ?)`,
		uuid.New().String(), agentID, peer.ID.String(), tenant)

	// --- assertions: one call per shared export function ----------------------
	for _, tc := range []struct {
		name string
		got  func() (int, error)
	}{
		{"agent context files", func() (int, error) {
			v, err := ExportAgentContextFiles(ctx, db, ag.ID)
			return len(v), err
		}},
		{"user context files", func() (int, error) {
			v, err := ExportUserContextFiles(ctx, db, ag.ID)
			return len(v), err
		}},
		{"memory documents", func() (int, error) {
			v, err := ExportMemoryDocuments(ctx, db, ag.ID)
			return len(v), err
		}},
		{"skill grants", func() (int, error) {
			v, err := ExportSkillGrants(ctx, db, ag.ID)
			return len(v), err
		}},
		{"mcp grants", func() (int, error) {
			v, err := ExportMCPGrants(ctx, db, ag.ID)
			return len(v), err
		}},
		{"config permissions", func() (int, error) {
			v, err := ExportConfigPermissions(ctx, db, ag.ID)
			return len(v), err
		}},
		{"user profiles", func() (int, error) {
			v, err := ExportUserProfiles(ctx, db, ag.ID)
			return len(v), err
		}},
		{"user overrides", func() (int, error) {
			v, err := ExportUserOverrides(ctx, db, ag.ID)
			return len(v), err
		}},
		{"evolution metrics", func() (int, error) {
			v, err := ExportEvolutionMetrics(ctx, db, ag.ID)
			return len(v), err
		}},
		{"evolution suggestions", func() (int, error) {
			v, err := ExportEvolutionSuggestions(ctx, db, ag.ID)
			return len(v), err
		}},
		{"agent links", func() (int, error) {
			v, err := ExportAgentLinks(ctx, db, ag.ID)
			return len(v), err
		}},
		{"mcp servers", func() (int, error) {
			v, err := ExportMCPServers(ctx, db)
			return len(v), err
		}},
		{"mcp grants with keys", func() (int, error) {
			v, err := ExportMCPGrantsWithKeys(ctx, db)
			return len(v), err
		}},
		{"team by lead", func() (int, error) {
			team, id, members, err := ExportTeamByLead(ctx, db, ag.ID)
			if err != nil {
				return 0, err
			}
			if team == nil || id == uuid.Nil {
				return 0, nil
			}
			return len(members), nil
		}},
		{"team tasks", func() (int, error) {
			v, err := ExportTeamTasks(ctx, db, uuid.MustParse(teamID))
			if err != nil || v == nil {
				return 0, err
			}
			return len(v.Tasks), nil
		}},
		{"team comments", func() (int, error) {
			v, err := ExportTeamComments(ctx, db, uuid.MustParse(teamID), []uuid.UUID{uuid.MustParse(taskID)})
			return len(v), err
		}},
		{"team events", func() (int, error) {
			v, err := ExportTeamEvents(ctx, db, uuid.MustParse(teamID), []uuid.UUID{uuid.MustParse(taskID)})
			return len(v), err
		}},
		{"vault documents", func() (int, error) {
			v, err := ExportVaultDocuments(ctx, db, ag.ID)
			return len(v), err
		}},
		{"vault links", func() (int, error) {
			v, err := ExportVaultLinks(ctx, db, ag.ID)
			return len(v), err
		}},
		{"episodic summaries", func() (int, error) {
			v, err := ExportEpisodicSummaries(ctx, db, ag.ID)
			return len(v), err
		}},
		{"cron jobs", func() (int, error) {
			v, err := ExportCronJobs(ctx, db, ag.ID)
			return len(v), err
		}},
		{"custom skills", func() (int, error) {
			v, err := ExportSkills(ctx, db, SkillExportSelection{})
			return len(v), err
		}},
		{"preview counts", func() (int, error) {
			v, err := ExportPreviewCounts(ctx, db, ag.ID)
			if err != nil {
				return 0, err
			}
			return v.CronJobs + v.EpisodicSummaries + v.VaultDocuments, nil
		}},
		{"skills preview", func() (int, error) {
			if _, err := ExportSkillsPreview(ctx, db); err != nil {
				return 0, err
			}
			return 1, nil
		}},
	} {
		got, err := tc.got()
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got == 0 {
			t.Errorf("%s: no rows exported (statement or scan failed silently)", tc.name)
		}
	}
}
