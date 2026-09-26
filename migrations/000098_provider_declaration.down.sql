-- Reverts 000098_provider_declaration.up.sql.
--
-- llm_models / provider_quirks are new tables with no predecessor; the
-- declaration columns are additive with defaults, so dropping them loses only
-- data that did not exist before the up migration (plus the copied exec_path).

DROP TABLE IF EXISTS provider_quirks;
DROP TABLE IF EXISTS llm_models;

ALTER TABLE llm_providers
    DROP COLUMN IF EXISTS exec_path,
    DROP COLUMN IF EXISTS settings_version,
    DROP COLUMN IF EXISTS auth_kind,
    DROP COLUMN IF EXISTS wire_api;
