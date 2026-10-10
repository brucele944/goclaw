package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type fakeVaultStoreBody struct {
	store.VaultStore
	stale    []store.VaultDocument
	replaced map[string][]store.VaultChunk // docID -> chunks
	hashes   map[string]string             // docID -> contentHash passed in
}

func (f *fakeVaultStoreBody) ListDocsNeedingBodyIndex(ctx context.Context, tenantID string, limit int) ([]store.VaultDocument, error) {
	return f.stale, nil
}

func (f *fakeVaultStoreBody) ReplaceDocumentChunks(ctx context.Context, tenantID, docID, contentHash string, chunks []store.VaultChunk) error {
	f.replaced[docID] = chunks
	f.hashes[docID] = contentHash
	return nil
}

func TestIndexStaleBodies_ChunksWorkspaceFiles(t *testing.T) {
	ws := t.TempDir()
	body := "# Prices\n| Deluxe Ocean Suite | 4,200,000 |\x00\nDeposit 30%\xff\n"
	if err := os.WriteFile(filepath.Join(ws, "prices.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &fakeVaultStoreBody{
		stale: []store.VaultDocument{
			{ID: "d1", Path: "prices.md", ContentHash: "h1"},
			{ID: "d2", Path: "gone.md", ContentHash: "h2"},
		},
		replaced: map[string][]store.VaultChunk{},
		hashes:   map[string]string{},
	}

	if !IndexStaleBodies(context.Background(), fs, "t1", ws) {
		t.Fatal("first pass should run")
	}

	chunks := fs.replaced["d1"]
	if len(chunks) != 1 {
		t.Fatalf("chunks = %+v, want 1", chunks)
	}
	want := "# Prices\n| Deluxe Ocean Suite | 4,200,000 |\nDeposit 30%"
	if chunks[0].Text != want {
		t.Fatalf("chunk text = %q, want %q", chunks[0].Text, want)
	}
	if chunks[0].StartLine != 1 || chunks[0].EndLine != 4 {
		t.Fatalf("lines = %d-%d, want 1-4", chunks[0].StartLine, chunks[0].EndLine)
	}
	if fs.hashes["d1"] != "h1" {
		t.Fatalf("hash = %q, want h1", fs.hashes["d1"])
	}
	// A missing file must not mark the doc as indexed.
	if _, ok := fs.replaced["d2"]; ok {
		t.Fatal("missing file should be skipped")
	}
}
