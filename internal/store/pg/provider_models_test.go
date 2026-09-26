package pg

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TestPGProviderStoreModelCRUD mirrors the SQLite model catalog test against a
// live Postgres so the two backends cannot drift. It skips cleanly when
// TEST_DATABASE_URL is unset (see hooksTestDB).
func TestPGProviderStoreModelCRUD(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider model store tests")
	}
	db := hooksTestDB(t)
	st := NewPGProviderStore(db, "")

	ctx := masterCtx()
	tenantBID, _ := seedTenantAndAgent(t, db)
	// Registered after seedTenantAndAgent, so it runs first (LIFO) and the tenant
	// can be dropped by the seeding cleanup.
	t.Cleanup(func() {
		db.Exec(`DELETE FROM provider_quirks WHERE tenant_id = $1`, tenantBID)
		db.Exec(`DELETE FROM llm_providers WHERE tenant_id = $1`, tenantBID)
	})
	ctxB := tenantScopedCtx(tenantBID)

	providerA := &store.LLMProviderData{
		Name:         "pg-models-a-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctx, providerA); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM llm_providers WHERE id = $1`, providerA.ID) })
	if providerA.WireAPI != store.WireAPIAnthropicMessages || providerA.AuthKind != store.AuthKindAPIKey || providerA.SettingsVersion != store.CurrentSettingsVersion {
		t.Fatalf("declaration derived from the brand = %q/%q/%d, want anthropic-messages/api_key/%d",
			providerA.WireAPI, providerA.AuthKind, providerA.SettingsVersion, store.CurrentSettingsVersion)
	}

	// Declaration columns survive a round trip.
	loaded, err := st.GetProvider(ctx, providerA.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if loaded.WireAPI != providerA.WireAPI || loaded.AuthKind != providerA.AuthKind || loaded.SettingsVersion != providerA.SettingsVersion {
		t.Errorf("round-tripped declaration = %q/%q/%d", loaded.WireAPI, loaded.AuthKind, loaded.SettingsVersion)
	}
	// Migration 000098 leaves exec_path NULL for non-cli providers; the read path
	// must survive it (ProviderColumns coalesces the column).
	if loaded.ExecPath != "" {
		t.Errorf("ExecPath = %q, want empty for a NULL column", loaded.ExecPath)
	}
	providers, err := st.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	found := false
	for _, p := range providers {
		if p.ID == providerA.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListProviders did not return the created provider")
	}

	// Legacy/seeded rows carry NULL for the nullable text columns; reads must not
	// fail on them (this is what the migration-time seed and pre-Go-written rows
	// look like).
	legacyID := uuid.Must(uuid.NewV7())
	if _, err := db.Exec(
		`INSERT INTO llm_providers (id, name, provider_type, enabled, settings, tenant_id)
		 VALUES ($1, $2, $3, true, '{}', $4)`,
		legacyID, "pg-legacy-null-"+legacyID.String()[:8], store.ProviderOpenAICompat, store.MasterTenantID,
	); err != nil {
		t.Fatalf("insert legacy NULL row: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM llm_providers WHERE id = $1`, legacyID) })
	legacy, err := st.GetProvider(ctx, legacyID)
	if err != nil {
		t.Fatalf("GetProvider on a legacy NULL row: %v", err)
	}
	if legacy.DisplayName != "" || legacy.APIBase != "" || legacy.APIKey != "" || legacy.ExecPath != "" {
		t.Errorf("legacy NULL row read as %q/%q/%q/%q, want empty strings",
			legacy.DisplayName, legacy.APIBase, legacy.APIKey, legacy.ExecPath)
	}
	if legacy.WireAPI != store.WireAPIOpenAICompletions || legacy.AuthKind != store.AuthKindAPIKey || legacy.SettingsVersion != 1 {
		t.Errorf("legacy row declarations = %q/%q/%d", legacy.WireAPI, legacy.AuthKind, legacy.SettingsVersion)
	}
	if _, err := st.ListAllProviders(ctx); err != nil {
		t.Fatalf("ListAllProviders with legacy NULL rows: %v", err)
	}
	if err := st.UpdateProvider(ctx, providerA.ID, map[string]any{
		"wire_api":  store.WireAPIAnthropicMessages,
		"auth_kind": store.AuthKindAPIKey,
		"exec_path": nil,
	}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	loaded, err = st.GetProvider(ctx, providerA.ID)
	if err != nil {
		t.Fatalf("GetProvider after update: %v", err)
	}
	if loaded.WireAPI != store.WireAPIAnthropicMessages {
		t.Errorf("wire_api after update = %q, want %q", loaded.WireAPI, store.WireAPIAnthropicMessages)
	}
	if err := st.UpdateProvider(ctx, providerA.ID, map[string]any{"wire_api": "nope"}); err == nil {
		t.Error("UpdateProvider with an unknown wire_api must be rejected")
	}
	if err := st.UpdateProvider(ctx, providerA.ID, map[string]any{"settings_version": 99}); err == nil {
		t.Error("UpdateProvider with an unknown settings_version must be rejected")
	}

	fetchedAt := time.Now().UTC()
	models := []store.LLMModel{
		{
			ModelID:       "claude-haiku-4-5",
			DisplayName:   ptrStr("Claude Haiku 4.5"),
			ContextWindow: ptrInt(200000),
			MaxTokens:     ptrInt(64000),
			CostInput:     ptrFloat(0.75),
			CostOutput:    ptrFloat(3),
			Enabled:       true,
		},
		{
			ModelID:       "claude-sonnet-4-5",
			Capabilities:  []byte(`{"tool_calling":true}`),
			Modalities:    []byte(`["text","image"]`),
			Authoritative: true,
			FetchedAt:     &fetchedAt,
			Source:        store.ModelSourceDiscovered,
			Enabled:       true,
		},
	}
	if err := st.UpsertModels(ctx, providerA.ID, models); err != nil {
		t.Fatalf("UpsertModels: %v", err)
	}

	got := pgListModels(t, st, ctx, providerA.ID)
	if len(got) != 2 {
		t.Fatalf("ListModels len = %d, want 2", len(got))
	}
	haiku := got[0]
	if haiku.ModelID != "claude-haiku-4-5" {
		t.Fatalf("ListModels order = %q first, want claude-haiku-4-5", haiku.ModelID)
	}
	if haiku.DisplayName == nil || *haiku.DisplayName != "Claude Haiku 4.5" {
		t.Errorf("DisplayName = %v", haiku.DisplayName)
	}
	if haiku.CostInput == nil || *haiku.CostInput != 0.75 || haiku.CostOutput == nil || *haiku.CostOutput != 3 {
		t.Errorf("cost round trip = %v / %v, want 0.75 / 3", haiku.CostInput, haiku.CostOutput)
	}
	if string(haiku.Modalities) != `["text"]` || string(haiku.Capabilities) != `{}` {
		t.Errorf("default JSON = %s / %s", haiku.Modalities, haiku.Capabilities)
	}
	assertJSONEqual(t, got[1].Reasoning, `{}`)
	assertJSONEqual(t, got[1].Compat, `{}`)
	if haiku.Source != store.ModelSourceBundled || !haiku.Enabled {
		t.Errorf("source/enabled = %q/%v", haiku.Source, haiku.Enabled)
	}
	if got[1].FetchedAt == nil {
		t.Error("FetchedAt lost on round trip")
	}
	// Postgres jsonb normalizes whitespace, so compare semantically.
	assertJSONEqual(t, got[1].Capabilities, `{"tool_calling":true}`)
	assertJSONEqual(t, got[1].Modalities, `["text","image"]`)

	haikuID := haiku.ID

	// Idempotent re-upsert keeps ids and the operator's enabled flag.
	if err := st.UpsertModels(ctx, providerA.ID, []store.LLMModel{
		{ModelID: "claude-haiku-4-5", DisplayName: ptrStr("refreshed"), Enabled: false},
		{ModelID: "claude-sonnet-4-5", Enabled: false},
	}); err != nil {
		t.Fatalf("UpsertModels (repeat): %v", err)
	}
	got = pgListModels(t, st, ctx, providerA.ID)
	if len(got) != 2 {
		t.Fatalf("ListModels after repeat len = %d, want 2", len(got))
	}
	if got[0].ID != haikuID {
		t.Errorf("upsert changed the row id: %s → %s", haikuID, got[0].ID)
	}
	if got[0].DisplayName == nil || *got[0].DisplayName != "refreshed" {
		t.Errorf("upsert did not refresh display_name: %v", got[0].DisplayName)
	}
	if !got[0].Enabled {
		t.Error("upsert must not clobber the enabled flag")
	}

	if err := st.SetModelEnabled(ctx, providerA.ID, "claude-haiku-4-5", false); err != nil {
		t.Fatalf("SetModelEnabled: %v", err)
	}
	got = pgListModels(t, st, ctx, providerA.ID)
	if got[0].Enabled || !got[1].Enabled {
		t.Errorf("SetModelEnabled affected the wrong rows: %v / %v", got[0].Enabled, got[1].Enabled)
	}
	if err := st.SetModelEnabled(ctx, providerA.ID, "does-not-exist", true); err == nil {
		t.Error("SetModelEnabled on an unknown model must error")
	}

	// Cross-tenant writes are rejected and tenant-scoped reads stay closed.
	if err := st.UpsertModels(ctxB, providerA.ID, []store.LLMModel{{ModelID: "intruder"}}); err == nil {
		t.Error("cross-tenant UpsertModels must be rejected")
	}
	if err := st.SetModelEnabled(ctxB, providerA.ID, "claude-sonnet-4-5", false); err == nil {
		t.Error("cross-tenant SetModelEnabled must be rejected")
	}
	if n := len(pgListModels(t, st, ctx, providerA.ID)); n != 2 {
		t.Errorf("ListModels len = %d after rejected cross-tenant writes, want 2", n)
	}
	if n := len(pgListModels(t, st, ctxB, providerA.ID)); n != 0 {
		t.Errorf("tenant B sees %d models of provider A, want 0", n)
	}

	// Positive control: tenant B writes its own provider's models.
	providerC := &store.LLMProviderData{
		Name:         "pg-models-c-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderAnthropicNative,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctxB, providerC); err != nil {
		t.Fatalf("CreateProvider (tenant B): %v", err)
	}
	if err := st.UpsertModels(ctxB, providerC.ID, []store.LLMModel{{ModelID: "tenant-b-model", Enabled: true}}); err != nil {
		t.Fatalf("UpsertModels (tenant B own provider): %v", err)
	}
	if n := len(pgListModels(t, st, ctxB, providerC.ID)); n != 1 {
		t.Errorf("tenant B ListModels len = %d, want 1", n)
	}
	if n := len(pgListModels(t, st, ctx, providerC.ID)); n != 0 {
		t.Errorf("master tenant sees %d models of tenant B provider, want 0", n)
	}
}

// TestPGProviderStoreListQuirks covers the quirk lookup precedence and isolation.
func TestPGProviderStoreListQuirks(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider model store tests")
	}
	db := hooksTestDB(t)
	st := NewPGProviderStore(db, "")

	tenantBID, _ := seedTenantAndAgent(t, db)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM provider_quirks WHERE tenant_id = $1`, tenantBID)
		db.Exec(`DELETE FROM llm_providers WHERE tenant_id = $1`, tenantBID)
	})
	ctx := tenantScopedCtx(store.MasterTenantID)
	ctxB := tenantScopedCtx(tenantBID)

	insert := func(t *testing.T, tenantID any, wireAPI, note string, enabled bool) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := db.Exec(
			`INSERT INTO provider_quirks (id, tenant_id, wire_api, endpoint_family, model_pattern, compat, note, source, enabled)
			 VALUES ($1, $2, $3, 'openai', $4, '{}', $5, 'bundled', $6)`,
			id, tenantID, wireAPI, "pattern-"+note, note, enabled,
		); err != nil {
			t.Fatalf("insert quirk %q: %v", note, err)
		}
		return id
	}
	t.Cleanup(func() {
		// Covers both bundled (tenant_id IS NULL) and this-test tenant rows.
		db.Exec(`DELETE FROM provider_quirks WHERE note LIKE 'pg-test-%'`)
	})

	insert(t, nil, store.WireAPIOpenAICompletions, "pg-test-bundled", true)
	insert(t, store.MasterTenantID, store.WireAPIOpenAICompletions, "pg-test-master", true)
	insert(t, nil, store.WireAPIOpenAICompletions, "pg-test-disabled", false)
	insert(t, nil, store.WireAPIGoogleGenerativeAI, "pg-test-other-wire", true)
	insert(t, tenantBID, store.WireAPIOpenAICompletions, "pg-test-tenant-b", true)

	got, err := st.ListQuirks(ctx, store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks: %v", err)
	}
	var notes []string
	for _, q := range got {
		if q.Note != nil && strings.HasPrefix(*q.Note, "pg-test-") {
			notes = append(notes, *q.Note)
		}
		if !q.Enabled {
			t.Errorf("ListQuirks returned a disabled row: %v", q.Note)
		}
		if q.WireAPI != store.WireAPIOpenAICompletions {
			t.Errorf("ListQuirks returned a row for wire_api %q", q.WireAPI)
		}
	}
	if len(notes) != 2 || notes[0] != "pg-test-master" || notes[1] != "pg-test-bundled" {
		t.Fatalf("ListQuirks notes = %v, want [pg-test-master pg-test-bundled]", notes)
	}

	bundledOnly, err := st.ListQuirks(context.Background(), store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks (no tenant): %v", err)
	}
	var bareNotes []string
	for _, q := range bundledOnly {
		if q.Note != nil && strings.HasPrefix(*q.Note, "pg-test-") {
			bareNotes = append(bareNotes, *q.Note)
		}
	}
	if len(bareNotes) != 1 || bareNotes[0] != "pg-test-bundled" {
		t.Fatalf("ListQuirks (no tenant) notes = %v, want [pg-test-bundled]", bareNotes)
	}

	tenantBQuirks, err := st.ListQuirks(ctxB, store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks (tenant B): %v", err)
	}
	var tenantBNotes []string
	for _, q := range tenantBQuirks {
		if q.Note != nil && strings.HasPrefix(*q.Note, "pg-test-") {
			tenantBNotes = append(tenantBNotes, *q.Note)
		}
	}
	if len(tenantBNotes) != 2 || tenantBNotes[0] != "pg-test-tenant-b" || tenantBNotes[1] != "pg-test-bundled" {
		t.Fatalf("ListQuirks (tenant B) notes = %v, want [pg-test-tenant-b pg-test-bundled]", tenantBNotes)
	}
}

func pgListModels(t *testing.T, st *PGProviderStore, ctx context.Context, providerID uuid.UUID) []store.LLMModel {
	t.Helper()
	models, err := st.ListModels(ctx, providerID)
	if err != nil {
		t.Fatalf("ListModels(%s): %v", providerID, err)
	}
	return models
}

func ptrStr(s string) *string     { return &s }
func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }

// assertJSONEqual compares two JSON documents semantically (jsonb normalizes
// whitespace and key order, so raw string equality would be brittle).
func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("unmarshal %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("unmarshal %s: %v", want, err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

// A provider created through any surface states its provider_type, not its wire
// protocol. The store must derive the declaration from that type's brand on both
// backends, or a freshly created anthropic/ollama/CLI/ChatGPT row is dispatched
// through the OpenAI-compatible transport.
func TestPGProviderStoreCreateDerivesDeclaration(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider store tests")
	}
	db := hooksTestDB(t)
	st := NewPGProviderStore(db, "")
	tenantBID, _ := seedTenantAndAgent(t, db)
	ctx := tenantScopedCtx(tenantBID)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM llm_providers WHERE tenant_id = $1`, tenantBID)
	})

	for _, tc := range []struct {
		providerType string
		wireAPI      string
		authKind     string
	}{
		{store.ProviderAnthropicNative, store.WireAPIAnthropicMessages, store.AuthKindAPIKey},
		{store.ProviderOllama, store.WireAPIOllamaNative, store.AuthKindNone},
		{store.ProviderOllamaCloud, store.WireAPIOllamaNative, store.AuthKindNone},
		{store.ProviderChatGPTOAuth, store.WireAPIOpenAICodexResponses, store.AuthKindOAuthBrowser},
		{store.ProviderClaudeCLI, store.WireAPICLIDelegated, store.AuthKindCLIDelegated},
		{store.ProviderVertex, store.WireAPIGoogleVertex, store.AuthKindServiceAccount},
		{store.ProviderDashScope, store.WireAPIOpenAICompletions, store.AuthKindAPIKey},
	} {
		p := &store.LLMProviderData{
			Name:         "pg-derive-" + tc.providerType + "-" + uuid.Must(uuid.NewV7()).String()[:8],
			ProviderType: tc.providerType,
			Enabled:      true,
		}
		if err := st.CreateProvider(ctx, p); err != nil {
			t.Fatalf("CreateProvider(%s): %v", tc.providerType, err)
		}
		loaded, err := st.GetProvider(ctx, p.ID)
		if err != nil {
			t.Fatalf("GetProvider(%s): %v", tc.providerType, err)
		}
		if loaded.WireAPI != tc.wireAPI || loaded.AuthKind != tc.authKind {
			t.Errorf("%s persisted as %q/%q, want %q/%q",
				tc.providerType, loaded.WireAPI, loaded.AuthKind, tc.wireAPI, tc.authKind)
		}
	}
}

// An explicitly stated declaration wins over the brand, so an operator can point
// a catalogued type at a different transport (e.g. an anthropic row behind an
// OpenAI-compatible proxy).
func TestPGProviderStoreCreateKeepsStatedDeclaration(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skipf("TEST_DATABASE_URL not set; skipping PG provider store tests")
	}
	db := hooksTestDB(t)
	st := NewPGProviderStore(db, "")
	tenantBID, _ := seedTenantAndAgent(t, db)
	ctx := tenantScopedCtx(tenantBID)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM llm_providers WHERE tenant_id = $1`, tenantBID)
	})

	p := &store.LLMProviderData{
		Name:         "pg-stated-" + uuid.Must(uuid.NewV7()).String()[:8],
		ProviderType: store.ProviderAnthropicNative,
		WireAPI:      store.WireAPIOpenAICompletions,
		AuthKind:     store.AuthKindNone,
		Enabled:      true,
	}
	if err := st.CreateProvider(ctx, p); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	loaded, err := st.GetProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if loaded.WireAPI != store.WireAPIOpenAICompletions || loaded.AuthKind != store.AuthKindNone {
		t.Errorf("stated declaration persisted as %q/%q", loaded.WireAPI, loaded.AuthKind)
	}
}
