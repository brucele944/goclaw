//go:build sqlite || sqliteonly

package sqlitestore

import (
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestSQLiteSchemaUpgrade_60_to_61_ProviderDeclaration verifies the v60 → v61
// migration mirrors migrations/000098_provider_declaration.up.sql:
//   - adds wire_api / auth_kind / exec_path / settings_version to llm_providers,
//   - creates llm_models (24 columns, UNIQUE(provider_id, model_id)) and provider_quirks,
//   - backfills wire_api + auth_kind from provider_type and copies api_base into
//     exec_path for cli-delegated rows while leaving api_base untouched (dual-read).
func TestSQLiteSchemaUpgrade_60_to_61_ProviderDeclaration(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// openTestDB applies the LATEST schema; strip the v61 additions so EnsureSchema
	// exercises the real v60 → v61 patch, then reset the recorded version.
	mustExec(t, db, `DROP TABLE llm_models`)
	mustExec(t, db, `DROP TABLE provider_quirks`)
	mustExec(t, db, `ALTER TABLE llm_providers DROP COLUMN wire_api`)
	mustExec(t, db, `ALTER TABLE llm_providers DROP COLUMN auth_kind`)
	mustExec(t, db, `ALTER TABLE llm_providers DROP COLUMN exec_path`)
	mustExec(t, db, `ALTER TABLE llm_providers DROP COLUMN settings_version`)
	mustExec(t, db, `UPDATE schema_version SET version = 60`)

	execPath := "/usr/local/bin/claude"
	cases := []struct {
		name         string
		providerType string
		apiBase      string
		wantWireAPI  string
		wantAuthKind string
		wantExecPath *string
	}{
		{"p-anthropic", store.ProviderAnthropicNative, "https://api.anthropic.com", store.WireAPIAnthropicMessages, store.AuthKindAPIKey, nil},
		{"p-chatgpt", store.ProviderChatGPTOAuth, "https://chatgpt.com/backend-api/codex", store.WireAPIOpenAICodexResponses, store.AuthKindOAuthBrowser, nil},
		{"p-claude-cli", store.ProviderClaudeCLI, execPath, store.WireAPICLIDelegated, store.AuthKindCLIDelegated, &execPath},
		{"p-acp", store.ProviderACP, "acp-binary", store.WireAPICLIDelegated, store.AuthKindCLIDelegated, strPtr("acp-binary")},
		{"p-vertex", store.ProviderVertex, "", store.WireAPIGoogleVertex, store.AuthKindServiceAccount, nil},
		{"p-ollama", store.ProviderOllama, "http://127.0.0.1:11434", store.WireAPIOllamaNative, store.AuthKindNone, nil},
		{"p-gemini", store.ProviderGeminiNative, "", store.WireAPIGoogleGenerativeAI, store.AuthKindAPIKey, nil},
		// Unknown brand: keeps the column default, exactly like the old registry switch.
		{"p-unknown", store.ProviderYesScale, "https://api.yescale.io/v1", store.WireAPIOpenAICompletions, store.AuthKindAPIKey, nil},
	}

	providerIDs := make(map[string]uuid.UUID, len(cases))
	for _, tc := range cases {
		id := uuid.Must(uuid.NewV7())
		providerIDs[tc.name] = id
		mustExec(t, db,
			`INSERT INTO llm_providers (id, name, provider_type, api_base, api_key, enabled, settings, tenant_id)
			 VALUES (?, ?, ?, ?, '', 1, '{}', ?)`,
			id, tc.name, tc.providerType, tc.apiBase, store.MasterTenantID,
		)
	}

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema (v60→61) failed: %v", err)
	}

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	// De-pinned from the literal 61: this test owns the v60 → v61 step, not the
	// latest version. Asserting `version == SchemaVersion` keeps the intent
	// (the v60 DB ends up on the current schema) without breaking every time a
	// later patch lands — see schema_migration_test.go for the version ladder.
	if SchemaVersion < 61 {
		t.Fatalf("SchemaVersion = %d, want >= 61 (the v60 → v61 patch must exist)", SchemaVersion)
	}
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}

	for _, column := range []string{"wire_api", "auth_kind", "exec_path", "settings_version"} {
		exists, err := sqliteColumnExists(db, "llm_providers", column)
		if err != nil {
			t.Fatalf("inspect llm_providers.%s: %v", column, err)
		}
		if !exists {
			t.Fatalf("llm_providers.%s missing after v60→v61 migration", column)
		}
	}

	for _, column := range []string{"provider_id", "model_id", "wire_api", "context_window", "max_tokens", "cost_input", "cost_cache_write", "modalities", "capabilities", "reasoning", "compat", "authoritative", "fetched_at", "static_fingerprint", "enabled"} {
		exists, err := sqliteColumnExists(db, "llm_models", column)
		if err != nil {
			t.Fatalf("inspect llm_models.%s: %v", column, err)
		}
		if !exists {
			t.Fatalf("llm_models.%s missing after v60→v61 migration", column)
		}
	}
	for _, column := range []string{"tenant_id", "wire_api", "endpoint_family", "model_pattern", "compat", "note", "source", "enabled"} {
		exists, err := sqliteColumnExists(db, "provider_quirks", column)
		if err != nil {
			t.Fatalf("inspect provider_quirks.%s: %v", column, err)
		}
		if !exists {
			t.Fatalf("provider_quirks.%s missing after v60→v61 migration", column)
		}
	}

	var modelColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('llm_models')`).Scan(&modelColumns); err != nil {
		t.Fatalf("count llm_models columns: %v", err)
	}
	if modelColumns != 24 {
		t.Errorf("llm_models column count = %d, want 24 (PG contract)", modelColumns)
	}

	for _, tc := range cases {
		var (
			wireAPI         string
			authKind        string
			gotExecPath     *string
			settingsVersion int
			apiBase         string
		)
		if err := db.QueryRow(
			`SELECT wire_api, auth_kind, exec_path, settings_version, api_base FROM llm_providers WHERE id = ?`,
			providerIDs[tc.name],
		).Scan(&wireAPI, &authKind, &gotExecPath, &settingsVersion, &apiBase); err != nil {
			t.Fatalf("read backfill for %s: %v", tc.name, err)
		}
		if wireAPI != tc.wantWireAPI {
			t.Errorf("%s (%s): wire_api = %q, want %q", tc.name, tc.providerType, wireAPI, tc.wantWireAPI)
		}
		if authKind != tc.wantAuthKind {
			t.Errorf("%s (%s): auth_kind = %q, want %q", tc.name, tc.providerType, authKind, tc.wantAuthKind)
		}
		if settingsVersion != 1 {
			t.Errorf("%s (%s): settings_version = %d, want 1", tc.name, tc.providerType, settingsVersion)
		}
		if tc.wantExecPath == nil {
			if gotExecPath != nil {
				t.Errorf("%s (%s): exec_path = %q, want NULL", tc.name, tc.providerType, *gotExecPath)
			}
		} else if gotExecPath == nil || *gotExecPath != *tc.wantExecPath {
			t.Errorf("%s (%s): exec_path = %v, want %q", tc.name, tc.providerType, gotExecPath, *tc.wantExecPath)
		}
		// api_base stays authoritative for one release (dual-read).
		if apiBase != tc.apiBase {
			t.Errorf("%s (%s): api_base = %q, want %q", tc.name, tc.providerType, apiBase, tc.apiBase)
		}
	}
}

func strPtr(s string) *string { return &s }
