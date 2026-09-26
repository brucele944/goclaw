//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

// TestSQLiteProviderStoreModelCRUD covers the per-model catalog write path:
// idempotent upsert on (provider_id, model_id), tenant-scoped listing, the
// single-row enable toggle, and the parent-tenant guard on writes.
func TestSQLiteProviderStoreModelCRUD(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	st := NewSQLiteProviderStore(db, "")
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)

	tenantBID := uuid.Must(uuid.NewV7())
	mustExec(t, db,
		`INSERT INTO tenants (id, name, slug, status) VALUES (?, 'Tenant B', ?, 'active')`,
		tenantBID, "tenant-b-"+tenantBID.String())
	ctxB := store.WithTenantID(context.Background(), tenantBID)

	createProvider := func(setupCtx context.Context, name string) *store.LLMProviderData {
		t.Helper()
		p := &store.LLMProviderData{
			Name:         name,
			ProviderType: store.ProviderAnthropicNative,
			APIBase:      "https://api.anthropic.com",
			Enabled:      true,
			// WireAPI/AuthKind/SettingsVersion intentionally unset: the store
			// derives them from ProviderAnthropicNative's brand.
		}
		if err := st.CreateProvider(setupCtx, p); err != nil {
			t.Fatalf("CreateProvider(%s): %v", name, err)
		}
		if p.WireAPI != store.WireAPIAnthropicMessages || p.AuthKind != store.AuthKindAPIKey || p.SettingsVersion != store.CurrentSettingsVersion {
			t.Fatalf("CreateProvider(%s) derived declaration = %q/%q/%d, want anthropic-messages/api_key/%d",
				name, p.WireAPI, p.AuthKind, p.SettingsVersion, store.CurrentSettingsVersion)
		}
		return p
	}

	providerA := createProvider(ctx, "models-a")
	providerB := createProvider(ctx, "models-b")

	// Legacy/seeded rows carry NULL for the nullable text columns (they predate the
	// Go layer writing empty strings); reads must not fail on them.
	legacyID := uuid.Must(uuid.NewV7())
	mustExec(t, db,
		`INSERT INTO llm_providers (id, name, provider_type, enabled, settings, tenant_id)
		 VALUES (?, 'legacy-null', ?, 1, '{}', ?)`,
		legacyID, store.ProviderOpenAICompat, store.MasterTenantID,
	)
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

	fetchedAt := time.Now().UTC().Truncate(time.Millisecond)
	input := []store.LLMModel{
		{
			ModelID:       "claude-haiku-4-5",
			DisplayName:   strPtr("Claude Haiku 4.5"),
			ContextWindow: intPtr(200000),
			MaxTokens:     intPtr(64000),
			CostInput:     floatPtr(0.75), // non-integral: stored as REAL
			CostOutput:    floatPtr(3),
			Tokenizer:     strPtr("claude"),
			Enabled:       true,
		},
		{
			ModelID:          "claude-sonnet-4-5",
			DisplayName:      strPtr("Claude Sonnet 4.5"),
			ContextWindow:    intPtr(200000),
			MaxContextWindow: intPtr(1000000),
			Capabilities:     []byte(`{"tool_calling":true}`),
			Modalities:       []byte(`["text","image"]`),
			Authoritative:    true,
			FetchedAt:        &fetchedAt,
			Source:           store.ModelSourceDiscovered,
			Enabled:          true,
		},
	}
	if err := st.UpsertModels(ctx, providerA.ID, input); err != nil {
		t.Fatalf("UpsertModels: %v", err)
	}

	got := listModels(t, st, ctx, providerA.ID)
	if len(got) != 2 {
		t.Fatalf("ListModels(providerA) len = %d, want 2", len(got))
	}
	// Ordered by model_id.
	haiku, sonnet := got[0], got[1]
	if haiku.ModelID != "claude-haiku-4-5" || sonnet.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("ListModels order = %q, %q", haiku.ModelID, sonnet.ModelID)
	}
	if haiku.ProviderID != providerA.ID {
		t.Errorf("ProviderID = %s, want %s", haiku.ProviderID, providerA.ID)
	}
	if haiku.DisplayName == nil || *haiku.DisplayName != "Claude Haiku 4.5" {
		t.Errorf("DisplayName = %v", haiku.DisplayName)
	}
	if haiku.ContextWindow == nil || *haiku.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %v", haiku.ContextWindow)
	}
	if haiku.MaxTokens == nil || *haiku.MaxTokens != 64000 {
		t.Errorf("MaxTokens = %v", haiku.MaxTokens)
	}
	if haiku.CostInput == nil || *haiku.CostInput != 0.75 {
		t.Errorf("CostInput = %v, want 0.75", haiku.CostInput)
	}
	if haiku.CostOutput == nil || *haiku.CostOutput != 3 {
		t.Errorf("CostOutput = %v, want 3", haiku.CostOutput)
	}
	if haiku.CostCacheRead != nil || haiku.CostCacheWrite != nil {
		t.Errorf("unset costs must stay NULL, got %v / %v", haiku.CostCacheRead, haiku.CostCacheWrite)
	}
	if string(haiku.Modalities) != `["text"]` {
		t.Errorf("default modalities = %s, want [\"text\"]", haiku.Modalities)
	}
	if string(haiku.Capabilities) != `{}` || string(haiku.Reasoning) != `{}` || string(haiku.Compat) != `{}` {
		t.Errorf("default JSON columns = %s / %s / %s", haiku.Capabilities, haiku.Reasoning, haiku.Compat)
	}
	if haiku.Source != store.ModelSourceBundled {
		t.Errorf("default source = %q, want %q", haiku.Source, store.ModelSourceBundled)
	}
	if haiku.FetchedAt != nil {
		t.Errorf("FetchedAt = %v, want nil", haiku.FetchedAt)
	}
	if !haiku.Enabled {
		t.Error("haiku should be enabled after insert")
	}
	if haiku.CreatedAt.IsZero() || haiku.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at not populated")
	}
	if sonnet.FetchedAt == nil || !sonnet.FetchedAt.Equal(fetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", sonnet.FetchedAt, fetchedAt)
	}
	if string(sonnet.Capabilities) != `{"tool_calling":true}` {
		t.Errorf("capabilities = %s", sonnet.Capabilities)
	}
	if sonnet.MaxContextWindow == nil || *sonnet.MaxContextWindow != 1000000 {
		t.Errorf("MaxContextWindow = %v", sonnet.MaxContextWindow)
	}
	if !sonnet.Authoritative || sonnet.Source != store.ModelSourceDiscovered {
		t.Errorf("authoritative/source = %v/%q", sonnet.Authoritative, sonnet.Source)
	}
	if len(listModels(t, st, ctx, providerB.ID)) != 0 {
		t.Error("ListModels(providerB) should be empty")
	}

	haikuID, haikuCreatedAt := haiku.ID, haiku.CreatedAt

	// Idempotent re-upsert: same (provider_id, model_id) → same rows/ids, refreshed
	// metadata, and the operator's enabled flag preserved.
	repeat := []store.LLMModel{
		{ModelID: "claude-haiku-4-5", DisplayName: strPtr("Claude Haiku 4.5 (refreshed)"), Enabled: false},
		{ModelID: "claude-sonnet-4-5", Enabled: false},
	}
	if err := st.UpsertModels(ctx, providerA.ID, repeat); err != nil {
		t.Fatalf("UpsertModels (repeat): %v", err)
	}
	got = listModels(t, st, ctx, providerA.ID)
	if len(got) != 2 {
		t.Fatalf("ListModels after repeat len = %d, want 2 (idempotent)", len(got))
	}
	haiku = got[0]
	if haiku.ID != haikuID {
		t.Errorf("upsert changed the row id: %s → %s", haikuID, haiku.ID)
	}
	if !haiku.CreatedAt.Equal(haikuCreatedAt) {
		t.Errorf("upsert changed created_at: %v → %v", haikuCreatedAt, haiku.CreatedAt)
	}
	if haiku.DisplayName == nil || *haiku.DisplayName != "Claude Haiku 4.5 (refreshed)" {
		t.Errorf("upsert did not refresh display_name: %v", haiku.DisplayName)
	}
	if !haiku.Enabled {
		t.Error("upsert must not clobber the enabled flag (toggle via SetModelEnabled)")
	}

	// SetModelEnabled flips exactly one row.
	if err := st.SetModelEnabled(ctx, providerA.ID, "claude-haiku-4-5", false); err != nil {
		t.Fatalf("SetModelEnabled: %v", err)
	}
	got = listModels(t, st, ctx, providerA.ID)
	if got[0].Enabled {
		t.Error("claude-haiku-4-5 should be disabled")
	}
	if !got[1].Enabled {
		t.Error("claude-sonnet-4-5 should still be enabled")
	}
	if err := st.SetModelEnabled(ctx, providerA.ID, "does-not-exist", true); err == nil {
		t.Error("SetModelEnabled on an unknown model must error")
	}

	// Cross-tenant writes are rejected and leave no rows behind.
	crossErr := st.UpsertModels(ctxB, providerA.ID, []store.LLMModel{{ModelID: "intruder"}})
	if crossErr == nil {
		t.Error("cross-tenant UpsertModels must be rejected")
	} else if !strings.Contains(crossErr.Error(), providerA.ID.String()) {
		t.Errorf("cross-tenant error = %v, want the i18n provider-not-found message", crossErr)
	}
	if err := st.SetModelEnabled(ctxB, providerA.ID, "claude-sonnet-4-5", false); err == nil {
		t.Error("cross-tenant SetModelEnabled must be rejected")
	}
	if n := len(listModels(t, st, ctx, providerA.ID)); n != 2 {
		t.Errorf("ListModels(providerA) len = %d after rejected cross-tenant writes, want 2", n)
	}
	if n := len(listModels(t, st, ctxB, providerA.ID)); n != 0 {
		t.Errorf("tenant B sees %d models of provider A, want 0", n)
	}
	got = listModels(t, st, ctx, providerA.ID)
	if !got[1].Enabled {
		t.Error("rejected cross-tenant write must not have disabled sonnet")
	}
	if err := st.UpsertModels(ctx, uuid.Must(uuid.NewV7()), []store.LLMModel{{ModelID: "orphan"}}); err == nil {
		t.Error("UpsertModels on a missing provider must error")
	}

	// Positive control: tenant B writes its own provider's models.
	providerC := createProvider(ctxB, "models-c")
	if err := st.UpsertModels(ctxB, providerC.ID, []store.LLMModel{{ModelID: "tenant-b-model", Enabled: true}}); err != nil {
		t.Fatalf("UpsertModels (tenant B own provider): %v", err)
	}
	if n := len(listModels(t, st, ctxB, providerC.ID)); n != 1 {
		t.Errorf("tenant B ListModels len = %d, want 1", n)
	}
	if n := len(listModels(t, st, ctx, providerC.ID)); n != 0 {
		t.Errorf("master tenant sees %d models of tenant B provider, want 0", n)
	}
}

// TestSQLiteProviderStoreListQuirks covers the quirk lookup: enabled rows only,
// bundled (tenant_id IS NULL) plus the caller's tenant, tenant rows first, and
// no leakage of other tenants' rows.
func TestSQLiteProviderStoreListQuirks(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	initSqlx(db)

	tenantBID := uuid.Must(uuid.NewV7())
	mustExec(t, db,
		`INSERT INTO tenants (id, name, slug, status) VALUES (?, 'Tenant B', ?, 'active')`,
		tenantBID, "tenant-b-quirk-"+tenantBID.String())

	insertQuirk := func(id, name string, tenantID any, wireAPI string, enabled bool, note string) {
		t.Helper()
		mustExec(t, db,
			`INSERT INTO provider_quirks (id, tenant_id, wire_api, endpoint_family, model_pattern, compat, note, source, enabled)
			 VALUES (?, ?, ?, 'openai', ?, '{"drop_reasoning":true}', ?, 'bundled', ?)`,
			id, tenantID, wireAPI, name, note, enabled,
		)
	}

	bundledID := uuid.Must(uuid.NewV7()).String()
	masterID := uuid.Must(uuid.NewV7()).String()
	disableID := uuid.Must(uuid.NewV7()).String()
	otherWireID := uuid.Must(uuid.NewV7()).String()
	tenantBQuirkID := uuid.Must(uuid.NewV7()).String()

	insertQuirk(bundledID, "gpt-5*", nil, store.WireAPIOpenAICompletions, true, "bundled")
	insertQuirk(masterID, "gpt-5.2*", store.MasterTenantID.String(), store.WireAPIOpenAICompletions, true, "master override")
	insertQuirk(disableID, "off*", nil, store.WireAPIOpenAICompletions, false, "disabled")
	insertQuirk(otherWireID, "gemini*", nil, store.WireAPIGoogleGenerativeAI, true, "other wire")
	insertQuirk(tenantBQuirkID, "b*", tenantBID.String(), store.WireAPIOpenAICompletions, true, "tenant B")

	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	got, err := NewSQLiteProviderStore(db, "").ListQuirks(ctx, store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListQuirks len = %d, want 2 (master override + bundled)", len(got))
	}
	if got[0].Note == nil || *got[0].Note != "master override" {
		t.Errorf("first quirk = %v, want the tenant row first (overrides bundled)", got[0].Note)
	}
	if got[1].Note == nil || *got[1].Note != "bundled" {
		t.Errorf("second quirk = %v, want the bundled row", got[1].Note)
	}
	for _, q := range got {
		if *q.Note == "disabled" || *q.Note == "other wire" || *q.Note == "tenant B" {
			t.Errorf("ListQuirks returned a row it must not: %v", *q.Note)
		}
	}

	// No tenant scope → bundled rows only (fail-closed).
	bundledOnly, err := NewSQLiteProviderStore(db, "").ListQuirks(context.Background(), store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks (no tenant): %v", err)
	}
	if len(bundledOnly) != 1 || bundledOnly[0].Note == nil || *bundledOnly[0].Note != "bundled" {
		t.Fatalf("ListQuirks (no tenant) = %v, want only the bundled row", bundledOnly)
	}

	ctxB := store.WithTenantID(context.Background(), tenantBID)
	tenantBQuirks, err := NewSQLiteProviderStore(db, "").ListQuirks(ctxB, store.WireAPIOpenAICompletions)
	if err != nil {
		t.Fatalf("ListQuirks (tenant B): %v", err)
	}
	if len(tenantBQuirks) != 2 {
		t.Fatalf("ListQuirks (tenant B) len = %d, want 2 (own row + bundled)", len(tenantBQuirks))
	}
	if tenantBQuirks[0].Note == nil || *tenantBQuirks[0].Note != "tenant B" {
		t.Errorf("tenant B first quirk = %v, want its own row", tenantBQuirks[0].Note)
	}
}

func listModels(t *testing.T, st *SQLiteProviderStore, ctx context.Context, providerID uuid.UUID) []store.LLMModel {
	t.Helper()
	models, err := st.ListModels(ctx, providerID)
	if err != nil {
		t.Fatalf("ListModels(%s): %v", providerID, err)
	}
	return models
}
