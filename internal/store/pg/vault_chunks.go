package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// vaultMaxEmbeddedChunks caps how many chunks of one document get an embedding.
// Every chunk is still full-text indexed; chunks past the cap only lack a vector.
const vaultMaxEmbeddedChunks = 64

// ReplaceDocumentChunks swaps the body chunks of a document and embeds them.
// No-op when the stored chunks were already built from contentHash.
func (s *PGVaultStore) ReplaceDocumentChunks(ctx context.Context, tenantID, docID, contentHash string, chunks []store.VaultChunk) error {
	tid, err := parseUUID(tenantID)
	if err != nil {
		return fmt.Errorf("vault replace chunks: tenant: %w", err)
	}
	did, err := parseUUID(docID)
	if err != nil {
		return fmt.Errorf("vault replace chunks: doc: %w", err)
	}

	var indexedHash sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT body_indexed_hash FROM vault_documents WHERE id = $1 AND tenant_id = $2`,
		did, tid,
	).Scan(&indexedHash)
	if err != nil {
		return fmt.Errorf("vault replace chunks: fetch doc: %w", err)
	}
	if indexedHash.Valid && indexedHash.String == contentHash {
		return nil
	}

	// Embed outside the transaction; a failed embed still leaves the chunks
	// searchable by keyword.
	embeddings := make([]*string, len(chunks))
	if s.embProvider != nil && len(chunks) > 0 {
		n := min(len(chunks), vaultMaxEmbeddedChunks)
		texts := make([]string, n)
		for i := range n {
			texts[i] = chunks[i].Text
		}
		vecs, embErr := s.embProvider.Embed(ctx, texts)
		if embErr != nil {
			slog.Warn("vault.chunks: embed", "doc", docID, "err", embErr)
		}
		for i := range min(len(vecs), n) {
			v := vectorToString(vecs[i])
			embeddings[i] = &v
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vault replace chunks: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM vault_document_chunks WHERE document_id = $1 AND tenant_id = $2`, did, tid,
	); err != nil {
		return fmt.Errorf("vault replace chunks: delete: %w", err)
	}
	for i, c := range chunks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vault_document_chunks
				(id, tenant_id, document_id, chunk_index, start_line, end_line, text, embedding)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.Must(uuid.NewV7()), tid, did, c.Index, c.StartLine, c.EndLine, c.Text, embeddings[i],
		); err != nil {
			return fmt.Errorf("vault replace chunks: insert: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE vault_documents SET body_indexed_hash = $1 WHERE id = $2 AND tenant_id = $3`,
		contentHash, did, tid,
	)
	if err != nil {
		return fmt.Errorf("vault replace chunks: mark indexed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("vault replace chunks: document deleted")
	}
	return tx.Commit()
}

// ListDocsNeedingBodyIndex returns text documents whose chunks are missing or stale.
// Media and binary documents are skipped: they have no text body to index.
func (s *PGVaultStore) ListDocsNeedingBodyIndex(ctx context.Context, tenantID string, limit int) ([]store.VaultDocument, error) {
	tid, err := parseUUID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("vault list needing body index: tenant: %w", err)
	}

	q := `SELECT id, tenant_id, agent_id, team_id, chat_id, scope, custom_scope, path, path_basename, title, doc_type,
			content_hash, summary, metadata, created_at, updated_at
		FROM vault_documents
		WHERE tenant_id = $1 AND doc_type NOT IN ('media', 'document')
			AND body_indexed_hash IS DISTINCT FROM content_hash
		ORDER BY created_at ASC`
	args := []any{tid}
	if limit > 0 {
		q += " LIMIT $2"
		args = append(args, limit)
	}

	var rows []vaultDocRow
	if err := pkgSqlxDB.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("vault.list_needing_body_index: %w", err)
	}
	return vaultDocRowsToDocs(rows), nil
}
