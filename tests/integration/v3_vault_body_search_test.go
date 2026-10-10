//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// Regression coverage for #1554: search must reach terms that only appear in the
// document body, and natural-language queries must not require every word.

// seedPriceSheet stores a doc whose summary generalizes away the room names,
// with the full table indexed as body chunks.
func seedPriceSheet(t *testing.T, ctx context.Context, vs *pg.PGVaultStore, tid, aid, path string) *store.VaultDocument {
	t.Helper()
	doc := makeVaultDoc(tid, aid, path, "Hotel price list")
	doc.Summary = "Lists room rates for 6 categories and the deposit policy."
	if err := vs.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	chunks := []store.VaultChunk{
		{Index: 0, StartLine: 1, EndLine: 3, Text: "| Room | Price |\n| Garden Twin | 1,800,000 |\n| Deluxe Ocean Suite | 4,200,000 |"},
		{Index: 1, StartLine: 4, EndLine: 5, Text: "Deposit is 30 percent, refundable up to 7 days before arrival."},
	}
	if err := vs.ReplaceDocumentChunks(ctx, tid, doc.ID, doc.ContentHash, chunks); err != nil {
		t.Fatalf("ReplaceDocumentChunks: %v", err)
	}
	return doc
}

func searchPaths(t *testing.T, vs *pg.PGVaultStore, opts store.VaultSearchOptions) []string {
	t.Helper()
	if opts.MaxResults == 0 {
		opts.MaxResults = 10
	}
	results, err := vs.Search(context.Background(), opts)
	if err != nil {
		t.Fatalf("Search %q: %v", opts.Query, err)
	}
	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.Document.Path
	}
	return paths
}

func TestStoreVault_Search_FindsBodyOnlyTerms(t *testing.T) {
	db := testDB(t)
	tenantID, agentID := seedTenantAgent(t, db)
	ctx := tenantCtx(tenantID)
	vs := pg.NewPGVaultStore(db) // no embedder: keyword search only
	tid, aid := tenantID.String(), agentID.String()

	seedPriceSheet(t, ctx, vs, tid, aid, "body/prices.md")
	other := makeVaultDoc(tid, aid, "body/other.md", "Staff rota")
	other.Summary = "Weekly shifts for front desk staff."
	if err := vs.UpsertDocument(ctx, other); err != nil {
		t.Fatalf("UpsertDocument other: %v", err)
	}

	paths := searchPaths(t, vs, store.VaultSearchOptions{Query: "Ocean Suite", TenantID: tid, AgentID: aid})
	if len(paths) != 1 || paths[0] != "body/prices.md" {
		t.Fatalf("search %q = %v, want only body/prices.md", "Ocean Suite", paths)
	}
}

func TestStoreVault_Search_NaturalLanguageQuery(t *testing.T) {
	db := testDB(t)
	tenantID, agentID := seedTenantAgent(t, db)
	ctx := tenantCtx(tenantID)
	vs := pg.NewPGVaultStore(db)
	tid, aid := tenantID.String(), agentID.String()

	seedPriceSheet(t, ctx, vs, tid, aid, "nl/prices.md")
	notes := makeVaultDoc(tid, aid, "nl/go.md", "Go Programming")
	notes.Summary = "Go concurrency goroutines channels"
	if err := vs.UpsertDocument(ctx, notes); err != nil {
		t.Fatalf("UpsertDocument notes: %v", err)
	}

	// Filler words are not in any document, so strict AND matching found nothing.
	paths := searchPaths(t, vs, store.VaultSearchOptions{Query: "what is the price of the Deluxe Ocean Suite?", TenantID: tid, AgentID: aid})
	if len(paths) == 0 || paths[0] != "nl/prices.md" {
		t.Fatalf("body query = %v, want nl/prices.md first", paths)
	}
	paths = searchPaths(t, vs, store.VaultSearchOptions{Query: "can you show me my notes about goroutines", TenantID: tid, AgentID: aid})
	if len(paths) == 0 || paths[0] != "nl/go.md" {
		t.Fatalf("summary query = %v, want nl/go.md first", paths)
	}
}

func TestStoreVault_Search_BodyChunksKeepFilters(t *testing.T) {
	db := testDB(t)
	tenantA, agentA := seedTenantAgent(t, db)
	tenantB, agentB := seedTenantAgent(t, db)
	agentA2 := seedAgentInTenant(t, db, tenantA)
	vs := pg.NewPGVaultStore(db)

	seedPriceSheet(t, tenantCtx(tenantA), vs, tenantA.String(), agentA.String(), "filters/prices.md")

	if paths := searchPaths(t, vs, store.VaultSearchOptions{Query: "Ocean Suite", TenantID: tenantB.String(), AgentID: agentB.String()}); len(paths) != 0 {
		t.Fatalf("other tenant sees body match: %v", paths)
	}
	if paths := searchPaths(t, vs, store.VaultSearchOptions{Query: "Ocean Suite", TenantID: tenantA.String(), AgentID: agentA2.String()}); len(paths) != 0 {
		t.Fatalf("other agent sees personal body match: %v", paths)
	}
}

func TestStoreVault_Search_ChunkEmbeddingMatch(t *testing.T) {
	db := testDB(t)
	tenantID, agentID := seedTenantAgent(t, db)
	ctx := tenantCtx(tenantID)
	vs := newVaultStore(db) // mock embedder: identical text gives identical vectors
	tid, aid := tenantID.String(), agentID.String()

	seedPriceSheet(t, ctx, vs, tid, aid, "vec/prices.md")
	for _, p := range []string{"vec/a.md", "vec/b.md", "vec/c.md"} {
		d := makeVaultDoc(tid, aid, p, "Filler "+p)
		d.Summary = "unrelated summary " + p
		if err := vs.UpsertDocument(ctx, d); err != nil {
			t.Fatalf("UpsertDocument %s: %v", p, err)
		}
	}

	query := "Deposit is 30 percent, refundable up to 7 days before arrival."
	paths := searchPaths(t, vs, store.VaultSearchOptions{Query: query, TenantID: tid, AgentID: aid})
	if len(paths) == 0 || paths[0] != "vec/prices.md" {
		t.Fatalf("chunk vector query = %v, want vec/prices.md first", paths)
	}
}

func TestStoreVault_ReplaceDocumentChunks_FollowsContentHash(t *testing.T) {
	db := testDB(t)
	tenantID, agentID := seedTenantAgent(t, db)
	ctx := tenantCtx(tenantID)
	vs := pg.NewPGVaultStore(db)
	tid, aid := tenantID.String(), agentID.String()

	doc := makeVaultDoc(tid, aid, "hash/doc.md", "Menu")
	if err := vs.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("UpsertDocument: %v", err)
	}
	needing := func() bool {
		docs, err := vs.ListDocsNeedingBodyIndex(ctx, tid, 0)
		if err != nil {
			t.Fatalf("ListDocsNeedingBodyIndex: %v", err)
		}
		for _, d := range docs {
			if d.ID == doc.ID {
				return true
			}
		}
		return false
	}
	replace := func(hash, text string) {
		if err := vs.ReplaceDocumentChunks(ctx, tid, doc.ID, hash, []store.VaultChunk{{Text: text}}); err != nil {
			t.Fatalf("ReplaceDocumentChunks: %v", err)
		}
	}
	query := func(q string) bool {
		return containsPath(searchPaths(t, vs, store.VaultSearchOptions{Query: q, TenantID: tid, AgentID: aid}), "hash/doc.md")
	}

	if !needing() {
		t.Fatal("new doc should need a body index")
	}
	replace(doc.ContentHash, "pho bo special")
	if needing() {
		t.Fatal("indexed doc should not need a body index")
	}

	// Same hash: chunks are kept, so the new text is not indexed.
	replace(doc.ContentHash, "banh mi special")
	if query("banh") || !query("pho") {
		t.Fatal("unchanged content_hash must keep the existing chunks")
	}

	// New content: the doc needs indexing again, then the old text is gone.
	if err := vs.UpdateHash(ctx, tid, doc.ID, "def456"); err != nil {
		t.Fatalf("UpdateHash: %v", err)
	}
	if !needing() {
		t.Fatal("changed doc should need a body index")
	}
	replace("def456", "banh mi special")
	if !query("banh") || query("pho") {
		t.Fatal("new content_hash must replace the chunks")
	}

	// Deleting the document removes its chunks with it.
	if err := vs.DeleteDocument(ctx, tid, aid, "hash/doc.md"); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM vault_document_chunks WHERE document_id = $1`, doc.ID).Scan(&n); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if n != 0 {
		t.Fatalf("chunks left after delete: %d", n)
	}
}
