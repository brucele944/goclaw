-- Provider health: durable cooldown state (provider rework, phase 5).
--
-- Why: cooldown lived only in an in-memory map (internal/providers/cooldown.go)
-- inside the fallback wrapper, so every gateway restart forgot that a provider
-- was cooling down and immediately retried the failing endpoint. Making the
-- state durable is also what gives the health surface (`goclaw providers health`,
-- GET /v1/providers/{id}/health) something to report.
--
-- Scope is inherited through provider_id (llm_models precedent, 000098): a health
-- row is visible exactly where its provider is visible, so there is no second
-- tenant column to keep in sync.
--
-- provider_health keeps at most one row per *failed* provider — no row means
-- "never failed", which is why the read path maps SQL NO ROWS to a zero value.
-- provider_error_counts is the error-class histogram; it is a separate table so
-- an increment is one upsert instead of a read-modify-write of a JSON blob.

CREATE TABLE IF NOT EXISTS provider_health (
    provider_id          UUID PRIMARY KEY REFERENCES llm_providers(id) ON DELETE CASCADE,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    cooldown_until       TIMESTAMPTZ,
    last_error_class     TEXT NOT NULL DEFAULT '',
    last_probe_at        TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS provider_error_counts (
    provider_id UUID NOT NULL REFERENCES llm_providers(id) ON DELETE CASCADE,
    error_class TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, error_class)
);
