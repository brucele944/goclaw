-- Provider declaration + per-model catalog (provider rework, phase 1).
--
-- Why: provider behaviour was encoded in Go (cmd/gateway_providers.go switches on
-- provider_type, quirks sniffed out of URLs/names) and model knowledge was either
-- hardcoded (internal/http/provider_models_catalog.go) or absent (one static
-- ProviderCapabilities per endpoint, Cost always zero). Declaring the wire
-- protocol, the auth shape, per-model metadata and quirks as data makes adding a
-- provider or a model a data change instead of a five-file code change.
--
-- llm_providers is already tenant-scoped (000027_tenant_foundation.up.sql:94,151,
-- 199,315-317) — this migration must NOT touch tenant_id, name uniqueness, or the
-- existing tenant index.

ALTER TABLE llm_providers
    ADD COLUMN IF NOT EXISTS wire_api         VARCHAR(40) NOT NULL DEFAULT 'openai-completions',
    ADD COLUMN IF NOT EXISTS auth_kind        VARCHAR(30) NOT NULL DEFAULT 'api_key',
    ADD COLUMN IF NOT EXISTS exec_path        TEXT,
    ADD COLUMN IF NOT EXISTS settings_version INTEGER     NOT NULL DEFAULT 1;

-- Backfill wire_api from the legacy provider_type. An explicit CASE (not a join)
-- so the mapping stays reviewable and greppable; providers of a brand that is not
-- listed keep the column default (openai-completions), which is what the old
-- registry switch did for every unknown type as well.
UPDATE llm_providers SET wire_api = CASE provider_type
    WHEN 'anthropic_native' THEN 'anthropic-messages'
    WHEN 'gemini_native'    THEN 'google-generative-ai'
    WHEN 'chatgpt_oauth'    THEN 'openai-codex-responses'
    WHEN 'vertex'           THEN 'google-vertex'
    WHEN 'claude_cli'       THEN 'cli-delegated'
    WHEN 'acp'              THEN 'cli-delegated'
    WHEN 'ollama'           THEN 'ollama-native'
    WHEN 'ollama_cloud'     THEN 'ollama-native'
    ELSE 'openai-completions'
END;

-- auth_kind mirrors how credentials are obtained today: browser OAuth for the
-- ChatGPT flow, subprocess-managed login for the CLI agents, service-account/ADC
-- for Vertex, keyless for local Ollama, and a static API key everywhere else.
UPDATE llm_providers SET auth_kind = CASE provider_type
    WHEN 'chatgpt_oauth' THEN 'oauth_browser'
    WHEN 'claude_cli'    THEN 'cli_delegated'
    WHEN 'acp'           THEN 'cli_delegated'
    WHEN 'vertex'        THEN 'service_account'
    WHEN 'ollama'        THEN 'none'
    WHEN 'ollama_cloud'  THEN 'none'
    ELSE 'api_key'
END;

-- cli-delegated providers currently keep the executable path in api_base. Copy it
-- so the wire layer can read exec_path; api_base stays authoritative for one
-- release (dual-read) until every call site is migrated.
UPDATE llm_providers SET exec_path = api_base
WHERE provider_type IN ('claude_cli', 'acp')
  AND api_base IS NOT NULL
  AND api_base <> '';

-- Per-model metadata. Scope is inherited through provider_id: a model row is
-- visible exactly where its provider is visible, so there is no second tenant
-- column to keep in sync.
CREATE TABLE IF NOT EXISTS llm_models (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
    provider_id        UUID NOT NULL REFERENCES llm_providers(id) ON DELETE CASCADE,
    model_id           TEXT NOT NULL,
    display_name       TEXT,
    wire_api           VARCHAR(40),
    context_window     INTEGER,
    max_tokens         INTEGER,
    max_context_window INTEGER,
    cost_input         NUMERIC(12,6),
    cost_output        NUMERIC(12,6),
    cost_cache_read    NUMERIC(12,6),
    cost_cache_write   NUMERIC(12,6),
    modalities         JSONB NOT NULL DEFAULT '["text"]',
    capabilities       JSONB NOT NULL DEFAULT '{}',
    reasoning          JSONB NOT NULL DEFAULT '{}',
    tokenizer          TEXT,
    compat             JSONB NOT NULL DEFAULT '{}',
    source             TEXT NOT NULL DEFAULT 'bundled',
    authoritative      BOOLEAN NOT NULL DEFAULT false,
    fetched_at         TIMESTAMPTZ,
    static_fingerprint TEXT,
    enabled            BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT llm_models_provider_model_key UNIQUE (provider_id, model_id)
);

CREATE INDEX IF NOT EXISTS idx_llm_models_provider ON llm_models (provider_id);
CREATE INDEX IF NOT EXISTS idx_llm_models_provider_enabled ON llm_models (provider_id) WHERE enabled;

-- Declared compatibility. NULL tenant_id = bundled/global row shipped with the
-- binary (the api_keys precedent, 000027:48-49); a tenant row overrides it.
CREATE TABLE IF NOT EXISTS provider_quirks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v7(),
    tenant_id       UUID REFERENCES tenants(id),
    wire_api        VARCHAR(40) NOT NULL,
    endpoint_family TEXT,
    model_pattern   TEXT,
    compat          JSONB NOT NULL DEFAULT '{}',
    note            TEXT,
    source          TEXT NOT NULL DEFAULT 'bundled',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_provider_quirks_lookup
    ON provider_quirks (wire_api, endpoint_family, model_pattern)
    WHERE enabled;
CREATE INDEX IF NOT EXISTS idx_provider_quirks_tenant ON provider_quirks (tenant_id) WHERE tenant_id IS NOT NULL;
