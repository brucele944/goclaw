package vault

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nextlevelbuilder/goclaw/internal/memory"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	// Same defaults as memory chunks.
	vaultBodyChunkLen     = 1000
	vaultBodyChunkOverlap = 200
	// SHORTCUT: bodies past 2 MiB are indexed only up to the cap. Stream the
	// file into chunks if people start keeping large exports in the vault.
	vaultBodyMaxBytes = 2 << 20
)

// chunkBody splits a file body into search chunks. Postgres TEXT rejects NUL
// bytes and invalid UTF-8, and a cut at vaultBodyMaxBytes can land mid-rune.
func chunkBody(raw []byte) []store.VaultChunk {
	if len(raw) > vaultBodyMaxBytes {
		raw = raw[:vaultBodyMaxBytes]
	}
	text := strings.ToValidUTF8(strings.ReplaceAll(string(raw), "\x00", ""), "")
	parts := memory.ChunkText(text, vaultBodyChunkLen, vaultBodyChunkOverlap)
	chunks := make([]store.VaultChunk, len(parts))
	for i, p := range parts {
		chunks[i] = store.VaultChunk{Index: i, StartLine: p.StartLine, EndLine: p.EndLine, Text: p.Text}
	}
	return chunks
}

// indexBody rebuilds the body chunks of one text document from its workspace
// file. The store skips the write when the chunks already match contentHash.
// Failures are logged here; the error only tells the caller to retry later.
func indexBody(ctx context.Context, vs store.VaultStore, tenantID, docID, contentHash, fullPath string) error {
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		slog.Warn("vault.body_index: read_file", "path", fullPath, "err", err)
		return err
	}
	if err := vs.ReplaceDocumentChunks(ctx, tenantID, docID, contentHash, chunkBody(raw)); err != nil {
		slog.Warn("vault.body_index: replace_chunks", "doc", docID, "err", err)
		return err
	}
	return nil
}

// bodyIndexRuns keeps one IndexStaleBodies pass per tenant.
var bodyIndexRuns sync.Map

// IndexStaleBodies chunks every text document whose body index is missing or
// older than its content. Docs indexed before chunking existed are never sent
// to the enrich worker again because their summary is already set, so rescan
// calls this to backfill them. No LLM calls: only reads and embeddings.
// Returns false when a pass for the tenant is already running.
func IndexStaleBodies(ctx context.Context, vs store.VaultStore, tenantID, workspace string) bool {
	if _, running := bodyIndexRuns.LoadOrStore(tenantID, struct{}{}); running {
		return false
	}
	defer bodyIndexRuns.Delete(tenantID)

	docs, err := vs.ListDocsNeedingBodyIndex(ctx, tenantID, 0)
	if err != nil {
		slog.Warn("vault.body_index: list", "tenant", tenantID, "err", err)
		return true
	}
	for _, doc := range docs {
		if ctx.Err() != nil {
			return true
		}
		_ = indexBody(ctx, vs, tenantID, doc.ID, doc.ContentHash, filepath.Join(workspace, doc.Path))
	}
	if len(docs) > 0 {
		slog.Info("vault.body_index: backfilled", "tenant", tenantID, "count", len(docs))
	}
	return true
}
