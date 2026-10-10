package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/eventbus"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"golang.org/x/sync/semaphore"
)

// fakeVaultStoreBodyRetry fails ReplaceDocumentChunks a set number of times,
// then stores the chunks. Everything else the enrich pipeline touches is a no-op.
type fakeVaultStoreBodyRetry struct {
	store.VaultStore
	doc       store.VaultDocument
	failsLeft int
	calls     int
	chunks    []store.VaultChunk
}

func (f *fakeVaultStoreBodyRetry) GetDocumentsByIDs(ctx context.Context, tenantID string, ids []string) ([]store.VaultDocument, error) {
	return []store.VaultDocument{f.doc}, nil
}

func (f *fakeVaultStoreBodyRetry) ReplaceDocumentChunks(ctx context.Context, tenantID, docID, contentHash string, chunks []store.VaultChunk) error {
	f.calls++
	if f.failsLeft > 0 {
		f.failsLeft--
		return errors.New("connection reset")
	}
	f.chunks = chunks
	return nil
}

func (f *fakeVaultStoreBodyRetry) UpdateSummaryAndReembed(ctx context.Context, tenantID, docID, summary string) error {
	return nil
}

func (f *fakeVaultStoreBodyRetry) FindSimilarDocs(ctx context.Context, tenantID, agentID, docID string, limit int) ([]store.VaultSearchResult, error) {
	return nil, nil
}

func (f *fakeVaultStoreBodyRetry) GetDocumentByID(ctx context.Context, tenantID, id string) (*store.VaultDocument, error) {
	return nil, errors.New("not needed")
}

// A transient body-index failure must not mark the doc as processed, or the
// next event with the same hash is dropped and the doc never gets chunks.
func TestEnrichWorker_RetriesBodyIndexAfterFailure(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "prices.md"), []byte("Deluxe Ocean Suite 4,200,000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tenantID := providers.MasterTenantID.String()
	docID := uuid.NewString()
	fs := &fakeVaultStoreBodyRetry{
		doc: store.VaultDocument{
			ID: docID, TenantID: tenantID, Path: "prices.md", DocType: "note",
			ContentHash: "h1", Summary: "Hotel room rates.",
		},
		failsLeft: 1,
	}
	reg := providers.NewRegistry(nil)
	reg.Register(&mockClassifyProvider{})
	w := &EnrichWorker{
		vault:       fs,
		registry:    reg,
		dedup:       make(map[string]string),
		sem:         semaphore.NewWeighted(enrichMaxConcurrent),
		progress:    NewEnrichProgress(nil),
		cancelFuncs: &sync.Map{},
	}
	event := eventbus.DomainEvent{Payload: eventbus.VaultDocUpsertedPayload{
		DocID: docID, TenantID: tenantID, Path: "prices.md", ContentHash: "h1", Workspace: ws,
	}}

	ctx := context.Background()
	if err := w.Handle(ctx, event); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if fs.chunks != nil {
		t.Fatal("chunks written although ReplaceDocumentChunks failed")
	}

	// Same event again, e.g. the next file save or sync tick.
	if err := w.Handle(ctx, event); err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	if len(fs.chunks) != 1 || fs.chunks[0].Text != "Deluxe Ocean Suite 4,200,000" {
		t.Fatalf("chunks after replay = %+v, want the file body", fs.chunks)
	}

	// Once indexed, the dedup gate applies again.
	if err := w.Handle(ctx, event); err != nil {
		t.Fatalf("third Handle: %v", err)
	}
	if fs.calls != 2 {
		t.Fatalf("ReplaceDocumentChunks calls = %d, want 2", fs.calls)
	}
}
