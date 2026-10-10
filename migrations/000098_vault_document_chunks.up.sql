-- Index vault document bodies in chunks (#1554). vault_documents.tsv and
-- embedding only cover title + path + summary, so terms that the auto-summary
-- drops (item names, prices, codes) could never be found by vault_search.
--
-- Chunks are derived data: the enrich worker rebuilds them from the workspace
-- file. body_indexed_hash records the content_hash the chunks were built from,
-- so unchanged files are not re-chunked or re-embedded, and rows with a NULL
-- or stale hash are picked up again by POST /v1/vault/rescan.
CREATE TABLE IF NOT EXISTS vault_document_chunks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    document_id UUID NOT NULL REFERENCES vault_documents(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    start_line  INT NOT NULL DEFAULT 0,
    end_line    INT NOT NULL DEFAULT 0,
    text        TEXT NOT NULL,
    embedding   vector(1536),
    -- 'simple' config (no stemming), same as vault_documents.tsv
    tsv         tsvector GENERATED ALWAYS AS (to_tsvector('simple', text)) STORED,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_vault_chunks_tenant ON vault_document_chunks(tenant_id);
CREATE INDEX IF NOT EXISTS idx_vault_chunks_tsv ON vault_document_chunks USING GIN(tsv);
CREATE INDEX IF NOT EXISTS idx_vault_chunks_embedding ON vault_document_chunks
    USING hnsw(embedding vector_cosine_ops);

ALTER TABLE vault_documents ADD COLUMN IF NOT EXISTS body_indexed_hash TEXT;
