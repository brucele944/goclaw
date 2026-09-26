package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/crypto"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// PGProviderStore implements store.ProviderStore backed by Postgres.
type PGProviderStore struct {
	db     *sql.DB
	encKey string // AES-256 encryption key for API keys (empty = plain text)
}

// providerSelectCols is the canonical llm_providers column list (declaration
// columns included), shared with the SQLite store to prevent drift.
const providerSelectCols = store.ProviderColumns

// providerQuirkSelectCols is the canonical provider_quirks column list.
const providerQuirkSelectCols = `id, tenant_id, wire_api, endpoint_family, model_pattern, compat, note, source, enabled, created_at, updated_at`

func NewPGProviderStore(db *sql.DB, encryptionKey string) *PGProviderStore {
	if encryptionKey != "" {
		slog.Info("provider store: API key encryption enabled")
	} else {
		slog.Warn("provider store: API key encryption disabled (plain text storage)")
	}
	return &PGProviderStore{db: db, encKey: encryptionKey}
}

func (s *PGProviderStore) CreateProvider(ctx context.Context, p *store.LLMProviderData) error {
	if p.ID == uuid.Nil {
		p.ID = store.GenNewID()
	}
	if err := store.NormalizeProviderDeclaration(p); err != nil {
		return err
	}

	apiKey := p.APIKey
	if s.encKey != "" && apiKey != "" {
		encrypted, err := crypto.Encrypt(apiKey, s.encKey)
		if err != nil {
			return fmt.Errorf("encrypt api key: %w", err)
		}
		apiKey = encrypted
	}

	settings := p.Settings
	if len(settings) == 0 {
		settings = []byte("{}")
	}

	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	tid := tenantIDForInsert(ctx)
	p.TenantID = tid
	// UPSERT: if provider with same (tenant_id, name) exists, update it and return its ID.
	// This handles orphaned providers left after agent deletion (#295).
	var actualID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO llm_providers (id, name, display_name, provider_type, api_base, api_key, enabled, settings, wire_api, auth_kind, exec_path, settings_version, created_at, updated_at, tenant_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 ON CONFLICT (tenant_id, name) DO UPDATE SET
			display_name = EXCLUDED.display_name, provider_type = EXCLUDED.provider_type,
			api_base = EXCLUDED.api_base, api_key = EXCLUDED.api_key,
			enabled = EXCLUDED.enabled, settings = EXCLUDED.settings,
			wire_api = EXCLUDED.wire_api, auth_kind = EXCLUDED.auth_kind,
			exec_path = EXCLUDED.exec_path, settings_version = EXCLUDED.settings_version,
			updated_at = EXCLUDED.updated_at
		 RETURNING id`,
		p.ID, p.Name, p.DisplayName, p.ProviderType, p.APIBase, apiKey, p.Enabled, settings,
		p.WireAPI, p.AuthKind, nilStr(p.ExecPath), p.SettingsVersion,
		now, now, tid,
	).Scan(&actualID)
	if err == nil {
		p.ID = actualID // sync in-memory ID with actual DB row
	}
	return err
}

func (s *PGProviderStore) GetProvider(ctx context.Context, id uuid.UUID) (*store.LLMProviderData, error) {
	tClause, tArgs, _, err := scopeClause(ctx, 2)
	if err != nil {
		return nil, err
	}
	var p store.LLMProviderData
	err = pkgSqlxDB.GetContext(ctx, &p,
		`SELECT `+providerSelectCols+`
		 FROM llm_providers WHERE id = $1`+tClause,
		append([]any{id}, tArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %s", id)
	}
	p.APIKey = s.decryptKey(p.APIKey, p.Name)
	return &p, nil
}

func (s *PGProviderStore) GetProviderByName(ctx context.Context, name string) (*store.LLMProviderData, error) {
	tClause, tArgs, _, err := scopeClause(ctx, 2)
	if err != nil {
		return nil, err
	}
	var p store.LLMProviderData
	err = pkgSqlxDB.GetContext(ctx, &p,
		`SELECT `+providerSelectCols+`
		 FROM llm_providers WHERE name = $1`+tClause,
		append([]any{name}, tArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	p.APIKey = s.decryptKey(p.APIKey, p.Name)
	return &p, nil
}

func (s *PGProviderStore) ListProviders(ctx context.Context) ([]store.LLMProviderData, error) {
	tClause, tArgs, _, err := scopeClause(ctx, 1)
	if err != nil {
		return nil, err
	}
	var result []store.LLMProviderData
	err = pkgSqlxDB.SelectContext(ctx, &result,
		`SELECT `+providerSelectCols+`
		 FROM llm_providers WHERE true`+tClause+` ORDER BY name`, tArgs...)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].APIKey = s.decryptKey(result[i].APIKey, result[i].Name)
	}
	return result, nil
}

// ListAllProviders returns all providers across all tenants. Server-internal only.
func (s *PGProviderStore) ListAllProviders(ctx context.Context) ([]store.LLMProviderData, error) {
	var result []store.LLMProviderData
	err := pkgSqlxDB.SelectContext(ctx, &result,
		`SELECT `+providerSelectCols+`
		 FROM llm_providers WHERE true ORDER BY name`)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].APIKey = s.decryptKey(result[i].APIKey, result[i].Name)
	}
	return result, nil
}

func (s *PGProviderStore) UpdateProvider(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if err := store.ValidateProviderUpdates(updates); err != nil {
		return err
	}
	if apiKey, ok := updates["api_key"]; ok && s.encKey != "" {
		if keyStr, ok := apiKey.(string); ok && keyStr != "" {
			encrypted, err := crypto.Encrypt(keyStr, s.encKey)
			if err != nil {
				return fmt.Errorf("encrypt api key: %w", err)
			}
			updates["api_key"] = encrypted
		}
	}
	if store.IsCrossTenant(ctx) {
		return execMapUpdate(ctx, s.db, "llm_providers", id, updates)
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return fmt.Errorf("tenant_id required")
	}
	return execMapUpdateWhereTenant(ctx, s.db, "llm_providers", updates, id, tid)
}

func (s *PGProviderStore) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	tClause, tArgs, _, err := scopeClause(ctx, 2)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Safe no-op after Commit (returns sql.ErrTxDone, ignored).
	defer tx.Rollback()

	// Defensive: disable heartbeats so the next scheduler tick after delete
	// cannot fire stale config. FK ON DELETE SET NULL clears provider_id auto.
	// Tenant-scope the UPDATE through agents to prevent cross-tenant side effects:
	// even though provider IDs are UUIDs (globally unique), an attacker who guessed
	// or leaked one could otherwise disable another tenant's heartbeats.
	// IsCrossTenant (master scope) bypasses scoping for legitimate cross-tenant admin.
	var updateQuery string
	var updateArgs []any
	if store.IsCrossTenant(ctx) {
		updateQuery = "UPDATE agent_heartbeats SET enabled = false WHERE provider_id = $1"
		updateArgs = []any{id}
	} else {
		tid := store.TenantIDFromContext(ctx)
		updateQuery = `UPDATE agent_heartbeats SET enabled = false
		               WHERE provider_id = $1
		                 AND agent_id IN (SELECT id FROM agents WHERE tenant_id = $2)`
		updateArgs = []any{id, tid}
	}
	res, err := tx.ExecContext(ctx, updateQuery, updateArgs...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Warn("heartbeat.provider_cleared",
			"provider_id", id, "heartbeats_disabled", n)
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM llm_providers WHERE id = $1"+tClause,
		append([]any{id}, tArgs...)...,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGProviderStore) decryptKey(apiKey, providerName string) string {
	if s.encKey != "" && apiKey != "" {
		decrypted, err := crypto.Decrypt(apiKey, s.encKey)
		if err != nil {
			slog.Warn("failed to decrypt provider API key", "provider", providerName, "error", err)
			return apiKey
		}
		return decrypted
	}
	return apiKey
}

// --- Per-model catalog + declared quirks (provider rework, phase 1) ---

// ensureProviderModelWrite verifies the parent provider exists and is visible to
// the caller before any llm_models write. llm_models has no tenant column —
// scope is inherited through provider_id — so this check is the tenant boundary
// for the per-model catalog.
func (s *PGProviderStore) ensureProviderModelWrite(ctx context.Context, providerID uuid.UUID) error {
	var ownerTenant uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT tenant_id FROM llm_providers WHERE id = $1`, providerID,
	).Scan(&ownerTenant)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrProviderNotFound(providerID)
		}
		return fmt.Errorf("lookup provider tenant: %w", err)
	}
	if store.IsMasterScope(ctx) {
		return nil
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil || ownerTenant != tid {
		slog.Warn("security.provider_model_tenant_mismatch",
			"provider_id", providerID, "caller_tenant", tid, "owner_tenant", ownerTenant)
		return store.ErrProviderNotFound(providerID)
	}
	return nil
}

func (s *PGProviderStore) ListModels(ctx context.Context, providerID uuid.UUID) ([]store.LLMModel, error) {
	tClause, tArgs, _, err := scopeClauseAlias(ctx, 2, "p")
	if err != nil {
		return nil, err
	}
	var result []store.LLMModel
	err = pkgSqlxDB.SelectContext(ctx, &result,
		`SELECT `+store.QualifiedModelColumns("m")+`
		   FROM llm_models m
		   JOIN llm_providers p ON p.id = m.provider_id
		  WHERE m.provider_id = $1`+tClause+`
		  ORDER BY m.model_id`,
		append([]any{providerID}, tArgs...)...,
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PGProviderStore) UpsertModels(ctx context.Context, providerID uuid.UUID, models []store.LLMModel) error {
	if len(models) == 0 {
		return nil
	}
	if err := s.ensureProviderModelWrite(ctx, providerID); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// One prepared statement reused for every row: no N+1, one round trip per model
	// and no string concatenation of user data.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO llm_models (
			id, provider_id, model_id, display_name, wire_api, context_window, max_tokens,
			max_context_window, cost_input, cost_output, cost_cache_read, cost_cache_write,
			modalities, capabilities, reasoning, tokenizer, compat, source, authoritative,
			fetched_at, static_fingerprint, enabled, created_at, updated_at
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		 ON CONFLICT (provider_id, model_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			wire_api = EXCLUDED.wire_api,
			context_window = EXCLUDED.context_window,
			max_tokens = EXCLUDED.max_tokens,
			max_context_window = EXCLUDED.max_context_window,
			cost_input = EXCLUDED.cost_input,
			cost_output = EXCLUDED.cost_output,
			cost_cache_read = EXCLUDED.cost_cache_read,
			cost_cache_write = EXCLUDED.cost_cache_write,
			modalities = EXCLUDED.modalities,
			capabilities = EXCLUDED.capabilities,
			reasoning = EXCLUDED.reasoning,
			tokenizer = EXCLUDED.tokenizer,
			compat = EXCLUDED.compat,
			source = EXCLUDED.source,
			authoritative = EXCLUDED.authoritative,
			fetched_at = EXCLUDED.fetched_at,
			static_fingerprint = EXCLUDED.static_fingerprint,
			updated_at = EXCLUDED.updated_at`)
	if err != nil {
		return fmt.Errorf("prepare model upsert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for i := range models {
		m := &models[i]
		store.FillModelDefaults(m)
		if m.ID == uuid.Nil {
			m.ID = store.GenNewID()
		}
		m.ProviderID = providerID
		if _, err := stmt.ExecContext(ctx,
			m.ID, providerID, m.ModelID, m.DisplayName, m.WireAPI,
			m.ContextWindow, m.MaxTokens, m.MaxContextWindow,
			m.CostInput, m.CostOutput, m.CostCacheRead, m.CostCacheWrite,
			m.Modalities, m.Capabilities, m.Reasoning,
			m.Tokenizer, m.Compat, m.Source, m.Authoritative,
			nilTime(m.FetchedAt), m.StaticFingerprint, m.Enabled, now, now,
		); err != nil {
			return fmt.Errorf("upsert model %q: %w", m.ModelID, err)
		}
	}
	return tx.Commit()
}

func (s *PGProviderStore) SetModelEnabled(ctx context.Context, providerID uuid.UUID, modelID string, enabled bool) error {
	if err := s.ensureProviderModelWrite(ctx, providerID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE llm_models SET enabled = $1, updated_at = NOW()
		  WHERE provider_id = $2 AND model_id = $3`,
		enabled, providerID, modelID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrProviderModelNotFound(providerID, modelID)
	}
	return nil
}

// UpsertQuirks inserts the bundled rows that are not present yet, keyed by
// (wire_api, endpoint_family, model_pattern) among global rows. It never touches
// an operator row (tenant-scoped or source='operator') and never updates an
// existing row, so an operator edit or disable survives every boot.
func (s *PGProviderStore) UpsertQuirks(ctx context.Context, quirks []store.ProviderQuirk) error {
	if len(quirks) == 0 {
		return nil
	}
	for _, q := range quirks {
		if q.WireAPI == "" {
			continue
		}
		var existing uuid.UUID
		err := s.db.QueryRowContext(ctx, `SELECT id FROM provider_quirks
			WHERE tenant_id IS NULL AND wire_api = $1
			  AND COALESCE(endpoint_family,'') = COALESCE($2,'')
			  AND COALESCE(model_pattern,'') = COALESCE($3,'') LIMIT 1`,
			q.WireAPI, q.EndpointFamily, q.ModelPattern).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup quirk %q: %w", q.WireAPI, err)
		}
		fragment := string(q.Compat)
		if fragment == "" {
			fragment = "{}"
		}
		source := q.Source
		if source == "" {
			source = store.ModelSourceBundled
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO provider_quirks (
				wire_api, endpoint_family, model_pattern, compat, note, source, enabled
			 ) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			q.WireAPI, q.EndpointFamily, q.ModelPattern, fragment, q.Note, source, true,
		); err != nil {
			return fmt.Errorf("insert quirk %q: %w", q.WireAPI, err)
		}
	}
	return nil
}

func (s *PGProviderStore) ListQuirks(ctx context.Context, wireAPI string) ([]store.ProviderQuirk, error) {
	var (
		query string
		args  []any
	)
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		// No tenant → bundled rows only (fail-closed).
		query = `SELECT ` + providerQuirkSelectCols + `
		           FROM provider_quirks
		          WHERE wire_api = $1 AND enabled AND tenant_id IS NULL
		          ORDER BY created_at, id`
		args = []any{wireAPI}
	} else {
		// A tenant row overrides a bundled row for the same wire/endpoint/model, so
		// tenant rows sort first (`tenant_id IS NULL` → false before true).
		query = `SELECT ` + providerQuirkSelectCols + `
		           FROM provider_quirks
		          WHERE wire_api = $1 AND enabled AND (tenant_id IS NULL OR tenant_id = $2)
		          ORDER BY (tenant_id IS NULL), created_at, id`
		args = []any{wireAPI, tid}
	}

	var result []store.ProviderQuirk
	if err := pkgSqlxDB.SelectContext(ctx, &result, query, args...); err != nil {
		return nil, err
	}
	return result, nil
}

// --- Provider health / durable cooldown (provider rework, phase 5) ---
//
// provider_health / provider_error_counts carry no tenant column: scope is
// inherited through provider_id (the llm_models precedent). The request path
// writes them with the *agent's* tenant, which for a master-scoped provider is
// legitimately not the provider's owner tenant, so a tenant clause on these
// writes would silently drop cooldown persistence. Every ID that reaches the
// runtime methods below came from a tenant-scoped provider read
// (GetProviderByName/GetProvider) — that read is the boundary. The operator
// mutations (ResetProviderHealth) keep the same parent-tenant guard the model
// writes use, because those are reachable straight from the admin API.

// providerHealthSelectCols is the canonical provider_health column list.
const providerHealthSelectCols = `provider_id, consecutive_failures, cooldown_until, last_error_class, last_probe_at, updated_at`

// providerHealthRow is the raw provider_health row before the histogram is merged in.
type providerHealthRow struct {
	ProviderID          uuid.UUID  `db:"provider_id"`
	ConsecutiveFailures int        `db:"consecutive_failures"`
	CooldownUntil       *time.Time `db:"cooldown_until"`
	LastErrorClass      string     `db:"last_error_class"`
	LastProbeAt         *time.Time `db:"last_probe_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
}

func (s *PGProviderStore) GetProviderHealth(ctx context.Context, providerID uuid.UUID) (*store.ProviderHealth, error) {
	health := store.NewProviderHealth(providerID)

	var row providerHealthRow
	err := pkgSqlxDB.GetContext(ctx, &row,
		`SELECT `+providerHealthSelectCols+` FROM provider_health WHERE provider_id = $1`,
		providerID,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No row = the provider never failed. Fall through to the histogram,
		// which is empty for exactly the same reason.
	case err != nil:
		return nil, fmt.Errorf("read provider health: %w", err)
	default:
		health.ConsecutiveFailures = row.ConsecutiveFailures
		health.CooldownUntil = row.CooldownUntil
		health.LastErrorClass = row.LastErrorClass
		health.LastProbeAt = row.LastProbeAt
		health.UpdatedAt = row.UpdatedAt
	}

	counts, err := s.providerErrorCounts(ctx, providerID)
	if err != nil {
		return nil, err
	}
	health.ErrorCounts = counts
	return health, nil
}

// providerErrorCounts reads the error-class histogram of one provider.
func (s *PGProviderStore) providerErrorCounts(ctx context.Context, providerID uuid.UUID) (map[string]int, error) {
	var rows []struct {
		ErrorClass string `db:"error_class"`
		Count      int    `db:"count"`
	}
	if err := pkgSqlxDB.SelectContext(ctx, &rows,
		`SELECT error_class, count FROM provider_error_counts WHERE provider_id = $1 ORDER BY error_class`,
		providerID,
	); err != nil {
		return nil, fmt.Errorf("read provider error counts: %w", err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.ErrorClass] = row.Count
	}
	return counts, nil
}

func (s *PGProviderStore) RecordProviderFailure(ctx context.Context, providerID uuid.UUID, errorClass string, cooldownUntil time.Time) error {
	errorClass = store.NormalizeErrorClass(errorClass)
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert both counters in one round trip each: consecutive_failures is
	// incremented in SQL (not read-modify-written in Go) so two gateway instances
	// failing the same provider concurrently cannot lose a failure.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, consecutive_failures, cooldown_until, last_error_class, updated_at)
		 VALUES ($1, 1, $2, $3, $4)
		 ON CONFLICT (provider_id) DO UPDATE SET
			consecutive_failures = provider_health.consecutive_failures + 1,
			cooldown_until = EXCLUDED.cooldown_until,
			last_error_class = EXCLUDED.last_error_class,
			updated_at = EXCLUDED.updated_at`,
		providerID, cooldownUntil.UTC(), errorClass, now,
	); err != nil {
		return fmt.Errorf("record provider failure: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_error_counts (provider_id, error_class, count)
		 VALUES ($1, $2, 1)
		 ON CONFLICT (provider_id, error_class) DO UPDATE SET
			count = provider_error_counts.count + 1`,
		providerID, errorClass,
	); err != nil {
		return fmt.Errorf("record provider error class: %w", err)
	}
	return tx.Commit()
}

func (s *PGProviderStore) RecordProviderSuccess(ctx context.Context, providerID uuid.UUID) error {
	// last_error_class and the histogram are deliberately kept: they are the
	// "what went wrong last time" history the health surface reports. Only the
	// live cooldown state is cleared.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, consecutive_failures, cooldown_until, updated_at)
		 VALUES ($1, 0, NULL, $2)
		 ON CONFLICT (provider_id) DO UPDATE SET
			consecutive_failures = 0,
			cooldown_until = NULL,
			updated_at = EXCLUDED.updated_at`,
		providerID, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("record provider success: %w", err)
	}
	return nil
}

func (s *PGProviderStore) MarkProviderProbe(ctx context.Context, providerID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, last_probe_at, updated_at)
		 VALUES ($1, $2, $2)
		 ON CONFLICT (provider_id) DO UPDATE SET
			last_probe_at = EXCLUDED.last_probe_at,
			updated_at = EXCLUDED.updated_at`,
		providerID, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("record provider probe: %w", err)
	}
	return nil
}

func (s *PGProviderStore) ResetProviderHealth(ctx context.Context, providerID uuid.UUID) error {
	if err := s.ensureProviderModelWrite(ctx, providerID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_error_counts WHERE provider_id = $1`, providerID); err != nil {
		return fmt.Errorf("reset provider error counts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_health WHERE provider_id = $1`, providerID); err != nil {
		return fmt.Errorf("reset provider health: %w", err)
	}
	return tx.Commit()
}
