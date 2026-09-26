-- Reverts 000100_provider_health.up.sql.
--
-- Both tables are new with no predecessor; dropping them loses only cooldown and
-- error-class history, which is re-derived from the next failure.

DROP TABLE IF EXISTS provider_error_counts;
DROP TABLE IF EXISTS provider_health;
