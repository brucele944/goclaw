package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// fakeStore is an in-memory ProviderStore subset that mirrors the real upsert
// semantics: idempotent on (provider_id, model_id), metadata refreshed on
// conflict, the operator's enabled flag preserved.
type fakeStore struct {
	mu       sync.Mutex
	models   map[uuid.UUID][]store.LLMModel
	upserts  int
	disabled []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{models: make(map[uuid.UUID][]store.LLMModel)}
}

func (s *fakeStore) ListModels(_ context.Context, providerID uuid.UUID) ([]store.LLMModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := append([]store.LLMModel(nil), s.models[providerID]...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ModelID < rows[j].ModelID })
	return rows, nil
}

func (s *fakeStore) UpsertModels(_ context.Context, providerID uuid.UUID, models []store.LLMModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts += len(models)
	rows := s.models[providerID]
	for _, in := range models {
		store.FillModelDefaults(&in)
		in.ProviderID = providerID
		found := false
		for i := range rows {
			if rows[i].ModelID != in.ModelID {
				continue
			}
			enabled := rows[i].Enabled
			id, createdAt := rows[i].ID, rows[i].CreatedAt
			rows[i] = in
			rows[i].Enabled = enabled
			rows[i].ID = id
			rows[i].CreatedAt = createdAt
			found = true
			break
		}
		if !found {
			if in.ID == uuid.Nil {
				in.ID = uuid.New()
			}
			in.Enabled = true
			rows = append(rows, in)
		}
	}
	s.models[providerID] = rows
	return nil
}

func (s *fakeStore) SetModelEnabled(_ context.Context, providerID uuid.UUID, modelID string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.models[providerID]
	for i := range rows {
		if rows[i].ModelID == modelID {
			rows[i].Enabled = enabled
			s.disabled = append(s.disabled, modelID)
			return nil
		}
	}
	return errors.New("model not found")
}

func (s *fakeStore) row(providerID uuid.UUID, modelID string) (store.LLMModel, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.models[providerID] {
		if r.ModelID == modelID {
			return r, true
		}
	}
	return store.LLMModel{}, false
}

func (s *fakeStore) count(providerID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.models[providerID])
}

// fakeDiscovery counts calls and returns a fixed result or error.
type fakeDiscovery struct {
	calls  int
	models []discovery.ModelInfo
	err    error
}

func (f *fakeDiscovery) Type() string { return discovery.TypeOpenAIModelsList }

func (f *fakeDiscovery) List(context.Context, discovery.ProviderRef) ([]discovery.ModelInfo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func testProvider(providerType string, settings string) *store.LLMProviderData {
	return &store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: uuid.New()},
		Name:         "test-" + providerType,
		ProviderType: providerType,
		WireAPI:      "openai-completions",
		Enabled:      true,
		Settings:     json.RawMessage(settings),
	}
}

func testRef(p *store.LLMProviderData) discovery.ProviderRef {
	return discovery.ProviderRef{
		ID:           p.ID,
		Name:         p.Name,
		ProviderType: p.ProviderType,
		WireAPI:      p.WireAPI,
		BaseURL:      "https://upstream.example/v1",
		APIKey:       "k",
		Settings:     p.Settings,
	}
}

// noRateLimit turns the in-process fetch interval off so a test can exercise the
// fingerprint/TTL rules without sleeping.
var noRateLimit = Options{MinInterval: -1}

func TestSyncReusesCacheUntilTheFingerprintChanges(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1", DisplayName: "m1"}, {ID: "m2", DisplayName: "m2"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("openai_compat", "")
	ref := testRef(p)

	opts := noRateLimit
	opts.Fetch = true
	opts.TTL = time.Hour

	first, err := svc.Sync(context.Background(), p, ref, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Fetched || fake.calls != 1 {
		t.Fatalf("first sync: fetched=%v calls=%d, want a fetch", first.Fetched, fake.calls)
	}
	if len(first.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(first.Rows))
	}
	row, ok := st.row(p.ID, "m1")
	if !ok {
		t.Fatal("m1 was not persisted")
	}
	if row.Source != store.ModelSourceDiscovered {
		t.Errorf("source = %q, want %q", row.Source, store.ModelSourceDiscovered)
	}
	if row.StaticFingerprint == nil || row.FetchedAt == nil {
		t.Fatalf("provenance missing: %+v", row)
	}

	// Same base URL, fresh cache: no second fetch.
	second, err := svc.Sync(context.Background(), p, ref, opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fetched || fake.calls != 1 {
		t.Fatalf("cached sync: fetched=%v calls=%d, want a cache hit", second.Fetched, fake.calls)
	}
	if len(second.Rows) != 2 {
		t.Fatalf("cached rows = %d, want 2", len(second.Rows))
	}

	// A changed api_base invalidates the fingerprint and forces a refetch.
	moved := ref
	moved.BaseURL = "https://elsewhere.example/v1"
	third, err := svc.Sync(context.Background(), p, moved, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Fetched || fake.calls != 2 {
		t.Fatalf("after api_base change: fetched=%v calls=%d, want a refetch", third.Fetched, fake.calls)
	}
	if third.Fingerprint == first.Fingerprint {
		t.Fatal("fingerprint did not change with the base URL")
	}
	row, _ = st.row(p.ID, "m1")
	if row.StaticFingerprint == nil || *row.StaticFingerprint != third.Fingerprint {
		t.Errorf("row fingerprint = %v, want the new one", row.StaticFingerprint)
	}
}

func TestSyncTTLExpiryRefetches(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("openai_compat", "")
	opts := noRateLimit
	opts.Fetch = true
	opts.TTL = time.Nanosecond

	if _, err := svc.Sync(context.Background(), p, testRef(p), opts); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.Sync(context.Background(), p, testRef(p), opts); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 {
		t.Fatalf("calls = %d, want 2 (the TTL expired)", fake.calls)
	}
}

func TestSyncRateLimitBoundsAutomaticRefreshes(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("openai_compat", "")
	// The TTL is already over when the second call runs, so only the in-process
	// interval can hold the refresh back.
	opts := Options{Fetch: true, MinInterval: time.Hour, TTL: time.Nanosecond}

	if _, err := svc.Sync(context.Background(), p, testRef(p), opts); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	res, err := svc.Sync(context.Background(), p, testRef(p), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want the automatic refresh rate-limited", fake.calls)
	}
	if !res.Stale {
		t.Error("a rate-limited refresh must report the catalog as stale")
	}
	if res.Err != nil {
		t.Errorf("a rate limit is not a failure: %v", res.Err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want the cached row", len(res.Rows))
	}

	// An explicit refresh is an operator action, not a storm: it goes through.
	opts.Refresh = true
	if _, err := svc.Sync(context.Background(), p, testRef(p), opts); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 2 {
		t.Fatalf("calls = %d, want the explicit refresh to fetch", fake.calls)
	}
}

func TestSyncDiscoveryFailureKeepsRowsAndMarksStale(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1", DisplayName: "m1"}, {ID: "m2"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("openai_compat", "")
	opts := noRateLimit
	opts.Fetch = true
	opts.TTL = time.Hour

	if _, err := svc.Sync(context.Background(), p, testRef(p), opts); err != nil {
		t.Fatal(err)
	}

	fake.err = discovery.Failed(discovery.ClassAuth, errors.New("401 unauthorized"))
	opts.Refresh = true
	res, err := svc.Sync(context.Background(), p, testRef(p), opts)
	if err != nil {
		t.Fatalf("a discovery failure must not be fatal: %v", err)
	}
	if !res.Stale {
		t.Error("stale = false, want true on a discovery failure")
	}
	if res.ErrorClass != discovery.ClassAuth {
		t.Errorf("error class = %q, want %q", res.ErrorClass, discovery.ClassAuth)
	}
	if res.Err == nil {
		t.Error("err = nil, want the classified failure")
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want the previous 2 (never an empty list)", len(res.Rows))
	}
	if st.count(p.ID) != 2 {
		t.Fatalf("store rows = %d, want 2", st.count(p.ID))
	}
}

func TestSyncWithoutFetchNeverTouchesTheUpstream(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("openai_compat", "")

	res, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("calls = %d, want none without Fetch", fake.calls)
	}
	if res.Fetched {
		t.Error("fetched = true, want false")
	}
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %d, want none (openai_compat ships no snapshot)", len(res.Rows))
	}
}

func TestSyncSeedsBundledCatalogWithoutClobberingOperatorRows(t *testing.T) {
	st := newFakeStore()
	svc := NewService(st, discovery.NewRegistry(nil))
	p := testProvider("zai", "")

	// An operator-owned override for a model the snapshot also knows.
	custom := "My Pinned GLM"
	if err := st.UpsertModels(context.Background(), p.ID, []store.LLMModel{{
		ModelID:     "glm-5.2",
		DisplayName: &custom,
		Source:      store.ModelSourceOperator,
		Enabled:     true,
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != len(discovery.Bundled("zai")) {
		t.Fatalf("rows = %d, want the whole bundled snapshot", len(res.Rows))
	}
	row, ok := st.row(p.ID, "glm-5.2")
	if !ok {
		t.Fatal("glm-5.2 missing")
	}
	if row.Source != store.ModelSourceOperator || row.DisplayName == nil || *row.DisplayName != custom {
		t.Fatalf("operator row was clobbered: %+v", row)
	}
	other, ok := st.row(p.ID, "glm-5.1")
	if !ok {
		t.Fatal("glm-5.1 missing")
	}
	if other.Source != store.ModelSourceBundled {
		t.Errorf("bundled row source = %q", other.Source)
	}
	if row0 := res.Rows[0]; row0.ModelID != "glm-5.2" {
		t.Errorf("first served row = %q, want the snapshot order to be kept", row0.ModelID)
	}
}

func TestSyncSeedingIsIdempotent(t *testing.T) {
	st := newFakeStore()
	svc := NewService(st, discovery.NewRegistry(nil))
	p := testProvider("zai", "")

	if _, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit); err != nil {
		t.Fatal(err)
	}
	first, _ := st.row(p.ID, "glm-5.1")
	afterFirst := st.upserts
	if _, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit); err != nil {
		t.Fatal(err)
	}
	if st.upserts != afterFirst {
		t.Errorf("second sync wrote again (%d → %d); seeding must be a no-op when nothing changed", afterFirst, st.upserts)
	}
	second, _ := st.row(p.ID, "glm-5.1")
	if first.ID != second.ID {
		t.Errorf("row id changed across syncs: %s → %s", first.ID, second.ID)
	}
	if st.count(p.ID) != len(discovery.Bundled("zai")) {
		t.Errorf("row count = %d", st.count(p.ID))
	}
}

func TestSyncStaticDiscoveryDoesNotWriteProvenance(t *testing.T) {
	st := newFakeStore()
	svc := NewService(st, discovery.NewRegistry(nil))
	p := testProvider("zai", "")
	if _, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit); err != nil {
		t.Fatal(err)
	}
	row, _ := st.row(p.ID, "glm-5.2")
	if row.FetchedAt != nil || row.StaticFingerprint != nil {
		t.Errorf("static snapshot rows must not carry fetch provenance: %+v", row)
	}
}

func TestSyncHidesDisabledRowsButKeepsThemStored(t *testing.T) {
	st := newFakeStore()
	svc := NewService(st, discovery.NewRegistry(nil))
	p := testProvider("zai", "")
	if _, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit); err != nil {
		t.Fatal(err)
	}
	if err := st.SetModelEnabled(context.Background(), p.ID, "glm-5.1", false); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Sync(context.Background(), p, testRef(p), noRateLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range res.Rows {
		if row.ModelID == "glm-5.1" {
			t.Fatal("a disabled row must not be served")
		}
	}
	if _, ok := st.row(p.ID, "glm-5.1"); !ok {
		t.Fatal("the disabled row must stay stored so its id remains resolvable")
	}
}

func TestEnsureBundledSeedsWithoutNetwork(t *testing.T) {
	st := newFakeStore()
	fake := &fakeDiscovery{models: []discovery.ModelInfo{{ID: "m1"}}}
	reg := discovery.NewRegistry(nil)
	reg.Register(fake)
	svc := NewService(st, reg)
	p := testProvider("aimlapi", "")

	rows, err := svc.EnsureBundled(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("calls = %d, want none", fake.calls)
	}
	want := discovery.Bundled("aimlapi")
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i].ModelID != want[i].ID {
			t.Fatalf("row %d = %q, want %q", i, rows[i].ModelID, want[i].ID)
		}
	}
	if all := st.count(p.ID); all != len(want) {
		t.Errorf("stored rows = %d, want %d", all, len(want))
	}
}

func TestFingerprintVariesWithWireAPIAndOperatorPins(t *testing.T) {
	base := Fingerprint("https://a.example/v1", "openai-completions", false, nil)
	if base == Fingerprint("https://b.example/v1", "openai-completions", false, nil) {
		t.Error("fingerprint must change with the base URL")
	}
	if base == Fingerprint("https://a.example/v1", "anthropic-messages", false, nil) {
		t.Error("fingerprint must change with the wire API")
	}
	if base == Fingerprint("https://a.example/v1", "openai-completions", true, nil) {
		t.Error("fingerprint must change with the authority flag")
	}
	if base == Fingerprint("https://a.example/v1", "openai-completions", false, []string{"pinned"}) {
		t.Error("fingerprint must change with operator pins")
	}
	if base == "" {
		t.Error("fingerprint must not be empty")
	}
}

func TestAuthoritativeFromSettings(t *testing.T) {
	cases := []struct {
		settings string
		want     bool
	}{
		{"", false},
		{"{}", false},
		{`{"discovered_models_authoritative":true}`, true},
		{`{"discovered_models_authoritative":false}`, false},
		{`{`, false},
	}
	for _, tc := range cases {
		if got := AuthoritativeFromSettings(json.RawMessage(tc.settings)); got != tc.want {
			t.Errorf("AuthoritativeFromSettings(%s) = %v, want %v", tc.settings, got, tc.want)
		}
	}
}

func TestPlanMergeGapFillOnlyAddsUnknownModels(t *testing.T) {
	existing := []store.LLMModel{
		{ModelID: "a", DisplayName: ptr("label a"), Source: store.ModelSourceBundled, Enabled: true},
	}
	found := []discovery.ModelInfo{
		{ID: "a", DisplayName: "upstream a"},
		{ID: "b", DisplayName: "b"},
	}
	plan := PlanMerge(existing, found, MergeOptions{Source: store.ModelSourceDiscovered})
	if len(plan.Disable) != 0 {
		t.Errorf("gap fill must not disable anything: %v", plan.Disable)
	}
	if len(plan.Upserts) != 1 || plan.Upserts[0].ModelID != "b" {
		t.Fatalf("upserts = %+v, want only the unknown model b", plan.Upserts)
	}
	if plan.Upserts[0].Source != store.ModelSourceDiscovered {
		t.Errorf("source = %q", plan.Upserts[0].Source)
	}
}

func TestPlanMergeAuthoritativeReplacesMembership(t *testing.T) {
	existing := []store.LLMModel{
		{ModelID: "a", DisplayName: ptr("label a"), Source: store.ModelSourceBundled, Enabled: true},
		{ModelID: "b", Source: store.ModelSourceBundled, Enabled: true},
	}
	found := []discovery.ModelInfo{
		{ID: "b", DisplayName: "b upstream", ContextWindow: parseIntPtr(128000)},
		{ID: "c"},
	}
	plan := PlanMerge(existing, found, MergeOptions{
		Authoritative: true, Source: store.ModelSourceDiscovered,
		TrackProvenance: true, Fingerprint: "fp", FetchedAt: time.Unix(1000, 0).UTC(),
	})
	if len(plan.Upserts) != 2 {
		t.Fatalf("upserts = %+v, want b refreshed and c inserted", plan.Upserts)
	}
	byID := map[string]store.LLMModel{}
	for _, row := range plan.Upserts {
		byID[row.ModelID] = row
	}
	if byID["b"].DisplayName == nil || *byID["b"].DisplayName != "b upstream" {
		t.Errorf("b was not refreshed: %+v", byID["b"])
	}
	if byID["b"].ContextWindow == nil || *byID["b"].ContextWindow != 128000 {
		t.Errorf("b context window = %v", byID["b"].ContextWindow)
	}
	if byID["b"].StaticFingerprint == nil || *byID["b"].StaticFingerprint != "fp" {
		t.Errorf("b provenance = %+v", byID["b"])
	}
	if byID["c"].Source != store.ModelSourceDiscovered || byID["c"].Authoritative != true {
		t.Errorf("c row = %+v", byID["c"])
	}
	if len(plan.Disable) != 1 || plan.Disable[0] != "a" {
		t.Fatalf("disable = %v, want the dropped bundled model a", plan.Disable)
	}
}

func TestPlanMergeNeverTouchesOperatorRows(t *testing.T) {
	existing := []store.LLMModel{
		{ModelID: "a", DisplayName: ptr("operator a"), Source: store.ModelSourceOperator, Enabled: true},
		{ModelID: "gone", Source: store.ModelSourceBundled, Enabled: true},
	}
	found := []discovery.ModelInfo{{ID: "a", DisplayName: "upstream a"}, {ID: "b"}}
	plan := PlanMerge(existing, found, MergeOptions{Authoritative: true, Source: store.ModelSourceDiscovered})
	for _, row := range plan.Upserts {
		if row.ModelID == "a" {
			t.Fatalf("the operator row was rewritten: %+v", row)
		}
	}
	for _, id := range plan.Disable {
		if id == "a" {
			t.Fatal("an operator row must never be disabled by discovery")
		}
	}
	if len(plan.Disable) != 1 || plan.Disable[0] != "gone" {
		t.Fatalf("disable = %v, want [gone]", plan.Disable)
	}
}

func TestPlanMergeIgnoresDuplicateAndEmptyIDs(t *testing.T) {
	plan := PlanMerge(nil, []discovery.ModelInfo{{ID: "a"}, {ID: "a"}, {ID: ""}}, MergeOptions{Source: store.ModelSourceDiscovered})
	if len(plan.Upserts) != 1 {
		t.Fatalf("upserts = %+v, want one row", plan.Upserts)
	}
}

func ptr(s string) *string   { return &s }
func parseIntPtr(v int) *int { return &v }
