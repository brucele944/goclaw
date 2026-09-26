//go:build sqlite || sqliteonly

package sqlitestore

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

// providerSelectCols is the canonical llm_providers column list (declaration
// columns included), shared with the Postgres store to prevent drift.
const providerSelectCols = store.ProviderColumns

// SQLiteProviderStore implements store.ProviderStore backed by SQLite.
type SQLiteProviderStore struct {
	db     *sql.DB
	encKey string // AES-256 encryption key for API keys (empty = plain text)
}

func NewSQLiteProviderStore(db *sql.DB, encryptionKey string) *SQLiteProviderStore {
	if encryptionKey != "" {
		slog.Info("provider store: API key encryption enabled")
	} else {
		slog.Warn("provider store: API key encryption disabled (plain text storage)")
	}
	return &SQLiteProviderStore{db: db, encKey: encryptionKey}
}

func (s *SQLiteProviderStore) CreateProvider(ctx context.Context, p *store.LLMProviderData) error {
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
	var actualID string
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO llm_providers (id, name, display_name, provider_type, api_base, api_key, enabled, settings, wire_api, auth_kind, exec_path, settings_version, created_at, updated_at, tenant_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, name) DO UPDATE SET
			display_name = excluded.display_name, provider_type = excluded.provider_type,
			api_base = excluded.api_base, api_key = excluded.api_key,
			enabled = excluded.enabled, settings = excluded.settings,
			wire_api = excluded.wire_api, auth_kind = excluded.auth_kind,
			exec_path = excluded.exec_path, settings_version = excluded.settings_version,
			updated_at = excluded.updated_at
		 RETURNING id`,
		p.ID, p.Name, p.DisplayName, p.ProviderType, p.APIBase, apiKey, p.Enabled, settings,
		p.WireAPI, p.AuthKind, nilStr(p.ExecPath), p.SettingsVersion,
		now, now, tid,
	).Scan(&actualID)
	if err == nil {
		if parsed, parseErr := uuid.Parse(actualID); parseErr == nil {
			p.ID = parsed // sync in-memory ID with actual DB row
		}
	}
	return err
}

func (s *SQLiteProviderStore) GetProvider(ctx context.Context, id uuid.UUID) (*store.LLMProviderData, error) {
	tClause, tArgs, err := scopeClause(ctx)
	if err != nil {
		return nil, err
	}
	var row providerRow
	args := append([]any{id}, tArgs...)
	err = pkgSqlxDB.GetContext(ctx, &row,
		`SELECT `+providerSelectCols+` FROM llm_providers WHERE id = ?`+tClause,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %s", id)
	}
	p := row.toLLMProviderData()
	p.APIKey = s.decryptKey(p.APIKey, p.Name)
	return &p, nil
}

func (s *SQLiteProviderStore) GetProviderByName(ctx context.Context, name string) (*store.LLMProviderData, error) {
	tClause, tArgs, err := scopeClause(ctx)
	if err != nil {
		return nil, err
	}
	var row providerRow
	args := append([]any{name}, tArgs...)
	err = pkgSqlxDB.GetContext(ctx, &row,
		`SELECT `+providerSelectCols+` FROM llm_providers WHERE name = ?`+tClause,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	p := row.toLLMProviderData()
	p.APIKey = s.decryptKey(p.APIKey, p.Name)
	return &p, nil
}

func (s *SQLiteProviderStore) ListProviders(ctx context.Context) ([]store.LLMProviderData, error) {
	tClause, tArgs, err := scopeClause(ctx)
	if err != nil {
		return nil, err
	}
	var rows []providerRow
	err = pkgSqlxDB.SelectContext(ctx, &rows,
		`SELECT `+providerSelectCols+` FROM llm_providers WHERE true`+tClause+` ORDER BY name`,
		tArgs...,
	)
	if err != nil {
		return nil, err
	}
	return s.convertAndDecryptProviders(rows), nil
}

// ListAllProviders returns all providers across all tenants. Server-internal only.
func (s *SQLiteProviderStore) ListAllProviders(ctx context.Context) ([]store.LLMProviderData, error) {
	var rows []providerRow
	err := pkgSqlxDB.SelectContext(ctx, &rows,
		`SELECT `+providerSelectCols+` FROM llm_providers ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	return s.convertAndDecryptProviders(rows), nil
}

func (s *SQLiteProviderStore) UpdateProvider(ctx context.Context, id uuid.UUID, updates map[string]any) error {
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

func (s *SQLiteProviderStore) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	tClause, tArgs, err := scopeClause(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Safe no-op after Commit.
	defer tx.Rollback()

	// Defensive: disable heartbeats so the next scheduler tick after delete
	// cannot fire stale config. FK ON DELETE SET NULL clears provider_id auto.
	// Tenant-scope the UPDATE through agents to prevent cross-tenant side effects.
	// IsCrossTenant (master scope) bypasses scoping for legitimate cross-tenant admin.
	var updateQuery string
	var updateArgs []any
	if store.IsCrossTenant(ctx) {
		updateQuery = "UPDATE agent_heartbeats SET enabled = 0 WHERE provider_id = ?"
		updateArgs = []any{id}
	} else {
		tid := store.TenantIDFromContext(ctx)
		updateQuery = `UPDATE agent_heartbeats SET enabled = 0
		               WHERE provider_id = ?
		                 AND agent_id IN (SELECT id FROM agents WHERE tenant_id = ?)`
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

	args := append([]any{id}, tArgs...)
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM llm_providers WHERE id = ?"+tClause,
		args...,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteProviderStore) decryptKey(apiKey, providerName string) string {
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

func (s *SQLiteProviderStore) convertAndDecryptProviders(rows []providerRow) []store.LLMProviderData {
	result := make([]store.LLMProviderData, 0, len(rows))
	for _, r := range rows {
		p := r.toLLMProviderData()
		p.APIKey = s.decryptKey(p.APIKey, p.Name)
		result = append(result, p)
	}
	return result
}

// --- Per-model catalog + declared quirks (provider rework, phase 1) ---

const providerQuirkSelectCols = `id, tenant_id, wire_api, endpoint_family, model_pattern, compat, note, source, enabled, created_at, updated_at`

// SQLite parameters must be plain driver values: the sqliteVal transform
// JSON-marshals pointers, so nullable columns are dereferenced explicitly here.
func nullStrValue(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullIntValue(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullFloatValue(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullTimeValue(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}

// ensureProviderModelWrite verifies the parent provider exists and is visible to
// the caller before any llm_models write. llm_models has no tenant column —
// scope is inherited through provider_id — so this check is the tenant boundary
// for the per-model catalog.
func (s *SQLiteProviderStore) ensureProviderModelWrite(ctx context.Context, providerID uuid.UUID) error {
	var ownerTenant uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT tenant_id FROM llm_providers WHERE id = ?`, providerID,
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

func (s *SQLiteProviderStore) ListModels(ctx context.Context, providerID uuid.UUID) ([]store.LLMModel, error) {
	tClause, tArgs, err := scopeClauseAlias(ctx, "p")
	if err != nil {
		return nil, err
	}
	var rows []llmModelRow
	err = pkgSqlxDB.SelectContext(ctx, &rows,
		`SELECT `+store.QualifiedModelColumns("m")+`
		   FROM llm_models m
		   JOIN llm_providers p ON p.id = m.provider_id
		  WHERE m.provider_id = ?`+tClause+`
		  ORDER BY m.model_id`,
		append([]any{providerID}, tArgs...)...,
	)
	if err != nil {
		return nil, err
	}
	models := make([]store.LLMModel, 0, len(rows))
	for i := range rows {
		models = append(models, rows[i].toLLMModel())
	}
	return models, nil
}

func (s *SQLiteProviderStore) UpsertModels(ctx context.Context, providerID uuid.UUID, models []store.LLMModel) error {
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
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider_id, model_id) DO UPDATE SET
			display_name = excluded.display_name,
			wire_api = excluded.wire_api,
			context_window = excluded.context_window,
			max_tokens = excluded.max_tokens,
			max_context_window = excluded.max_context_window,
			cost_input = excluded.cost_input,
			cost_output = excluded.cost_output,
			cost_cache_read = excluded.cost_cache_read,
			cost_cache_write = excluded.cost_cache_write,
			modalities = excluded.modalities,
			capabilities = excluded.capabilities,
			reasoning = excluded.reasoning,
			tokenizer = excluded.tokenizer,
			compat = excluded.compat,
			source = excluded.source,
			authoritative = excluded.authoritative,
			fetched_at = excluded.fetched_at,
			static_fingerprint = excluded.static_fingerprint,
			updated_at = excluded.updated_at`)
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
			m.ID, providerID, m.ModelID, nullStrValue(m.DisplayName), nullStrValue(m.WireAPI),
			nullIntValue(m.ContextWindow), nullIntValue(m.MaxTokens), nullIntValue(m.MaxContextWindow),
			nullFloatValue(m.CostInput), nullFloatValue(m.CostOutput),
			nullFloatValue(m.CostCacheRead), nullFloatValue(m.CostCacheWrite),
			string(m.Modalities), string(m.Capabilities), string(m.Reasoning),
			nullStrValue(m.Tokenizer), string(m.Compat), m.Source, m.Authoritative,
			nullTimeValue(m.FetchedAt), nullStrValue(m.StaticFingerprint), m.Enabled, now, now,
		); err != nil {
			return fmt.Errorf("upsert model %q: %w", m.ModelID, err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteProviderStore) SetModelEnabled(ctx context.Context, providerID uuid.UUID, modelID string, enabled bool) error {
	if err := s.ensureProviderModelWrite(ctx, providerID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE llm_models SET enabled = ?, updated_at = ?
		  WHERE provider_id = ? AND model_id = ?`,
		enabled, time.Now().UTC(), providerID, modelID,
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
func (s *SQLiteProviderStore) UpsertQuirks(ctx context.Context, quirks []store.ProviderQuirk) error {
	if len(quirks) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, q := range quirks {
		if q.WireAPI == "" {
			continue
		}
		var existing string
		err := s.db.QueryRowContext(ctx, `SELECT id FROM provider_quirks
			WHERE tenant_id IS NULL AND wire_api = ?
			  AND COALESCE(endpoint_family,'') = COALESCE(?,'')
			  AND COALESCE(model_pattern,'') = COALESCE(?,'') LIMIT 1`,
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
				id, tenant_id, wire_api, endpoint_family, model_pattern, compat, note, source, enabled, created_at, updated_at
			 ) VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			store.GenNewID(), q.WireAPI, nullStrValue(q.EndpointFamily), nullStrValue(q.ModelPattern),
			fragment, nullStrValue(q.Note), source, true, now, now,
		); err != nil {
			return fmt.Errorf("insert quirk %q: %w", q.WireAPI, err)
		}
	}
	return nil
}

func (s *SQLiteProviderStore) ListQuirks(ctx context.Context, wireAPI string) ([]store.ProviderQuirk, error) {
	var (
		query string
		args  []any
	)
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		// No tenant → bundled rows only (fail-closed).
		query = `SELECT ` + providerQuirkSelectCols + `
		           FROM provider_quirks
		          WHERE wire_api = ? AND enabled = 1 AND tenant_id IS NULL
		          ORDER BY created_at, id`
		args = []any{wireAPI}
	} else {
		// A tenant row overrides a bundled row for the same wire/endpoint/model, so
		// tenant rows sort first (`tenant_id IS NULL` → 0 before 1).
		query = `SELECT ` + providerQuirkSelectCols + `
		           FROM provider_quirks
		          WHERE wire_api = ? AND enabled = 1 AND (tenant_id IS NULL OR tenant_id = ?)
		          ORDER BY (tenant_id IS NULL), created_at, id`
		args = []any{wireAPI, tid}
	}

	var rows []providerQuirkRow
	if err := pkgSqlxDB.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	quirks := make([]store.ProviderQuirk, 0, len(rows))
	for i := range rows {
		quirks = append(quirks, rows[i].toProviderQuirk())
	}
	return quirks, nil
}

// --- Provider health / durable cooldown (provider rework, phase 5) ---
//
// Mirrors the Postgres implementation (see internal/store/pg/providers.go).
// Scope is inherited through provider_id, so the runtime writes carry no tenant
// clause: the ID they receive came from a tenant-scoped provider read, and the
// request path's tenant is legitimately not the owner tenant of a master-scoped
// provider. The operator mutation (ResetProviderHealth) keeps the parent-tenant
// guard the model writes use.

// providerHealthSelectCols is the canonical provider_health column list.
const providerHealthSelectCols = `provider_id, consecutive_failures, cooldown_until, last_error_class, last_probe_at, updated_at`

// providerHealthRow is the raw provider_health row before the histogram is merged in.
type providerHealthRow struct {
	ProviderID          uuid.UUID      `db:"provider_id"`
	ConsecutiveFailures int            `db:"consecutive_failures"`
	CooldownUntil       nullSqliteTime `db:"cooldown_until"`
	LastErrorClass      string         `db:"last_error_class"`
	LastProbeAt         nullSqliteTime `db:"last_probe_at"`
	UpdatedAt           sqliteTime     `db:"updated_at"`
}

func (s *SQLiteProviderStore) GetProviderHealth(ctx context.Context, providerID uuid.UUID) (*store.ProviderHealth, error) {
	health := store.NewProviderHealth(providerID)

	var row providerHealthRow
	err := pkgSqlxDB.GetContext(ctx, &row,
		`SELECT `+providerHealthSelectCols+` FROM provider_health WHERE provider_id = ?`,
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
		health.LastErrorClass = row.LastErrorClass
		health.UpdatedAt = row.UpdatedAt.Time
		if row.CooldownUntil.Valid {
			cooldownUntil := row.CooldownUntil.Time
			health.CooldownUntil = &cooldownUntil
		}
		if row.LastProbeAt.Valid {
			lastProbeAt := row.LastProbeAt.Time
			health.LastProbeAt = &lastProbeAt
		}
	}

	counts, err := s.providerErrorCounts(ctx, providerID)
	if err != nil {
		return nil, err
	}
	health.ErrorCounts = counts
	return health, nil
}

// providerErrorCounts reads the error-class histogram of one provider.
func (s *SQLiteProviderStore) providerErrorCounts(ctx context.Context, providerID uuid.UUID) (map[string]int, error) {
	var rows []struct {
		ErrorClass string `db:"error_class"`
		Count      int    `db:"count"`
	}
	if err := pkgSqlxDB.SelectContext(ctx, &rows,
		`SELECT error_class, count FROM provider_error_counts WHERE provider_id = ? ORDER BY error_class`,
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

func (s *SQLiteProviderStore) RecordProviderFailure(ctx context.Context, providerID uuid.UUID, errorClass string, cooldownUntil time.Time) error {
	errorClass = store.NormalizeErrorClass(errorClass)
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert both counters in one statement each: consecutive_failures is
	// incremented in SQL (not read-modify-written in Go) so two gateway instances
	// failing the same provider concurrently cannot lose a failure.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, consecutive_failures, cooldown_until, last_error_class, updated_at)
		 VALUES (?, 1, ?, ?, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
			consecutive_failures = provider_health.consecutive_failures + 1,
			cooldown_until = excluded.cooldown_until,
			last_error_class = excluded.last_error_class,
			updated_at = excluded.updated_at`,
		providerID, cooldownUntil.UTC(), errorClass, now,
	); err != nil {
		return fmt.Errorf("record provider failure: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_error_counts (provider_id, error_class, count)
		 VALUES (?, ?, 1)
		 ON CONFLICT(provider_id, error_class) DO UPDATE SET
			count = provider_error_counts.count + 1`,
		providerID, errorClass,
	); err != nil {
		return fmt.Errorf("record provider error class: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteProviderStore) RecordProviderSuccess(ctx context.Context, providerID uuid.UUID) error {
	// last_error_class and the histogram are deliberately kept: they are the
	// "what went wrong last time" history the health surface reports. Only the
	// live cooldown state is cleared.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, consecutive_failures, cooldown_until, updated_at)
		 VALUES (?, 0, NULL, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
			consecutive_failures = 0,
			cooldown_until = NULL,
			updated_at = excluded.updated_at`,
		providerID, time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("record provider success: %w", err)
	}
	return nil
}

func (s *SQLiteProviderStore) MarkProviderProbe(ctx context.Context, providerID uuid.UUID) error {
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO provider_health (provider_id, last_probe_at, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
			last_probe_at = excluded.last_probe_at,
			updated_at = excluded.updated_at`,
		providerID, now, now,
	); err != nil {
		return fmt.Errorf("record provider probe: %w", err)
	}
	return nil
}

func (s *SQLiteProviderStore) ResetProviderHealth(ctx context.Context, providerID uuid.UUID) error {
	if err := s.ensureProviderModelWrite(ctx, providerID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_error_counts WHERE provider_id = ?`, providerID); err != nil {
		return fmt.Errorf("reset provider error counts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_health WHERE provider_id = ?`, providerID); err != nil {
		return fmt.Errorf("reset provider health: %w", err)
	}
	return tx.Commit()
}
