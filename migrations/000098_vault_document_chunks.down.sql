ALTER TABLE vault_documents DROP COLUMN IF EXISTS body_indexed_hash;
DROP TABLE IF EXISTS vault_document_chunks;
