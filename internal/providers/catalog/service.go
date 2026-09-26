// Package catalog owns the per-provider model catalogue stored in `llm_models`.
//
// Two sources feed it: the bundled snapshot GoClaw ships
// (internal/providers/discovery.Bundled) and live discovery against the
// provider's upstream. This package decides when to reuse the cached row set
// (fingerprint + TTL), merges a fresh discovery result into it without ever
// destroying an operator's row or a persisted model id, and records provenance
// (source, fetched_at, static_fingerprint).
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// Store is the slice of store.ProviderStore the catalogue needs.
type Store interface {
	ListModels(ctx context.Context, providerID uuid.UUID) ([]store.LLMModel, error)
	UpsertModels(ctx context.Context, providerID uuid.UUID, models []store.LLMModel) error
	SetModelEnabled(ctx context.Context, providerID uuid.UUID, modelID string, enabled bool) error
}

// QuirkStore is the optional slice the quirk seeding needs. Kept separate from
// Store so existing narrow fakes keep compiling; a store that does not implement
// it is a no-op (the bundled seeds still resolve at build time).
type QuirkStore interface {
	UpsertQuirks(ctx context.Context, quirks []store.ProviderQuirk) error
}

// EnsureQuirks persists the bundled quirk seeds. Idempotent by construction:
// UpsertQuirks inserts only rows whose (wire_api, endpoint_family,
// model_pattern) key is absent, so an operator edit or disable is never
// clobbered and repeated calls are free of duplicates.
func (s *Service) EnsureQuirks(ctx context.Context) error {
	qs, ok := s.store.(QuirkStore)
	if !ok {
		return nil
	}
	seeds := compat.Bundled()
	rows := make([]store.ProviderQuirk, 0, len(seeds))
	for _, seed := range seeds {
		rows = append(rows, store.ProviderQuirk{
			WireAPI:        seed.WireAPI,
			EndpointFamily: optionalString(seed.EndpointFamily),
			ModelPattern:   optionalString(seed.ModelPattern),
			Compat:         seed.Compat,
			Note:           optionalString(seed.Note),
			Source:         store.ModelSourceBundled,
			Enabled:        true,
		})
	}
	if err := qs.UpsertQuirks(ctx, rows); err != nil {
		slog.Warn("providers.quirks.seed_failed", "error", err)
		return err
	}
	return nil
}

func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// Defaults for the cache policy. A cached row set is reused while its
// static_fingerprint matches and it is younger than DefaultTTL; DefaultMinInterval
// bounds how often one provider can be re-fetched in-process (refresh storms).
const (
	DefaultTTL         = 10 * time.Minute
	DefaultMinInterval = 30 * time.Second
)

// Options tunes one Sync call.
type Options struct {
	// Fetch allows the call to reach the upstream when the cache is stale or its
	// fingerprint no longer matches. false keeps the call database-only (the
	// gateway-wide /v1/models listing never fetches).
	Fetch bool
	// Refresh forces a fetch even when the cache is fresh (CLI --refresh, UI
	// retry button).
	Refresh bool
	// TTL is the cache reuse window; 0 means DefaultTTL, negative means "never
	// expire" (used by tests).
	TTL time.Duration
	// MinInterval bounds the in-process fetch frequency per provider.
	MinInterval time.Duration
}

func (o Options) ttl() time.Duration {
	switch {
	case o.TTL == 0:
		return DefaultTTL
	case o.TTL < 0:
		return 0
	default:
		return o.TTL
	}
}

func (o Options) minInterval() time.Duration {
	if o.MinInterval == 0 {
		return DefaultMinInterval
	}
	if o.MinInterval < 0 {
		return 0
	}
	return o.MinInterval
}

// Result is the outcome of a Sync call.
type Result struct {
	// Rows is the effective catalogue: the store rows of the provider, ordered
	// for display (discovery order first, then model id). Enabled rows only.
	Rows []store.LLMModel
	// Models is the last inspection result — the live discovery list when a fetch
	// happened, the bundled snapshot otherwise. It is the fallback the caller
	// serves when the store holds no rows (read-only or unwired store).
	Models []discovery.ModelInfo
	// Stale reports that the served rows are known not to be current because a
	// needed refresh could not be used (upstream failure, rate limit).
	Stale bool
	// Err is a classified discovery failure. It is never fatal: the caller serves
	// Rows/Models and surfaces the error to the UI.
	Err error
	// ErrorClass is the failure class of Err (discovery.ClassOf).
	ErrorClass string
	// Fetched reports whether this call reached the upstream.
	Fetched bool
	// Fingerprint is the static_fingerprint this call computed.
	Fingerprint string
}

// Service seeds, refreshes and reads model catalogues.
type Service struct {
	store Store
	reg   *discovery.Registry

	mu          sync.Mutex
	lastAttempt map[uuid.UUID]time.Time
}

// NewService builds a catalogue service. A nil registry disables discovery
// (seeding and cached reads still work), which keeps the handler usable in
// tests and in editions without outbound network access.
func NewService(s Store, reg *discovery.Registry) *Service {
	return &Service{store: s, reg: reg, lastAttempt: make(map[uuid.UUID]time.Time)}
}

// Sync returns the provider's effective catalogue.
//
// It always seeds the bundled snapshot first (idempotent, operator rows are never
// clobbered), then — when opts.Fetch is set and the fingerprint cache says the
// rows are not current — refreshes from the provider's upstream discovery and
// merges the result. A discovery failure is returned in Result.Err with
// Stale=true and the previous rows intact; the returned error is reserved for
// store failures, which the caller must surface as a real error.
func (s *Service) Sync(ctx context.Context, p *store.LLMProviderData, ref discovery.ProviderRef, opts Options) (Result, error) {
	existing, err := s.store.ListModels(ctx, p.ID)
	if err != nil {
		return Result{}, err
	}

	rows := existing
	if _, seedErr := s.seedBundled(ctx, p, existing); seedErr != nil {
		// Serving the catalogue must not depend on write access (and a read-only
		// replica must still answer): log and continue with what we read.
		slog.Warn("providers.catalog.seed_failed", "provider", p.Name, "provider_id", p.ID, "error", seedErr)
	} else if refreshed, listErr := s.store.ListModels(ctx, p.ID); listErr == nil {
		rows = refreshed
	}

	authoritative := AuthoritativeFromSettings(p.Settings)
	fingerprint := Fingerprint(ref.BaseURL, p.WireAPI, authoritative, OperatorModelIDs(rows))
	bundled := discovery.Bundled(p.ProviderType)
	result := Result{
		Rows:        orderedRows(validRows(rows), bundled),
		Fingerprint: fingerprint,
		Models:      bundled,
	}

	if !opts.Fetch {
		// Caller asked for the cached catalogue only: never touch the network.
		return result, nil
	}

	static := discovery.ResolveType(p.ProviderType, p.WireAPI, p.Settings) == discovery.TypeStatic
	if static {
		// A snapshot is served from memory: nothing to rate-limit, nothing that
		// can be stale. Running it through the merge keeps one code path for
		// rows, membership and the bundled source value.
		return s.mergeSnapshot(ctx, p, ref, rows, result, authoritative)
	}

	state := cacheStateOf(rows, fingerprint, opts.ttl(), time.Now().UTC())
	switch {
	case !opts.Refresh && state.fresh():
		// The cached set belongs to this configuration and is young enough.
		return result, nil
	case !opts.Refresh && state.tracked && !state.matches:
		// The cache was invalidated (api_base or a pin changed): the cached rows
		// describe a different upstream, so refresh immediately rather than
		// serving them. This is self-limiting — once the rows carry the new
		// fingerprint the TTL governs the next fetch.
	case !opts.Refresh && !s.allowAttempt(p.ID, opts.minInterval(), time.Now().UTC()):
		// Automatic refresh, rate-limited: the rows are fine, just not current.
		slog.Debug("providers.catalog.refresh_rate_limited", "provider", p.Name, "provider_id", p.ID)
		result.Stale = true
		return result, nil
	}

	found, derr := s.discover(ctx, p, ref)
	if derr != nil {
		slog.Warn("providers.catalog.discovery_failed", "provider", p.Name, "provider_id", p.ID,
			"class", discovery.ClassOf(derr), "error", derr)
		result.Err = derr
		result.ErrorClass = discovery.ClassOf(derr)
		result.Stale = true
		return result, nil
	}
	result.Fetched = true
	result.Models = found

	plan := PlanMerge(rows, found, MergeOptions{
		Authoritative:   authoritative,
		Source:          store.ModelSourceDiscovered,
		TrackProvenance: true,
		Fingerprint:     fingerprint,
		FetchedAt:       time.Now().UTC(),
	})
	return s.commit(ctx, p, found, plan, result)
}

// mergeSnapshot applies the bundled snapshot through the same merge path as a
// live listing (its source stays `bundled` and it carries no fetch provenance).
func (s *Service) mergeSnapshot(ctx context.Context, p *store.LLMProviderData, ref discovery.ProviderRef, rows []store.LLMModel, result Result, authoritative bool) (Result, error) {
	found, err := s.discover(ctx, p, ref)
	if err != nil {
		// No snapshot for this provider type: report it, but never as staleness of
		// a served set — nothing was served from it.
		result.Err = err
		result.ErrorClass = discovery.ClassOf(err)
		return result, nil
	}
	result.Models = found
	plan := PlanMerge(rows, found, MergeOptions{
		Authoritative: authoritative,
		Source:        store.ModelSourceBundled,
	})
	return s.commit(ctx, p, found, plan, result)
}

// commit writes a merge plan and re-reads the catalogue.
func (s *Service) commit(ctx context.Context, p *store.LLMProviderData, found []discovery.ModelInfo, plan MergePlan, result Result) (Result, error) {
	if err := s.apply(ctx, p, plan); err != nil {
		result.Err = err
		result.ErrorClass = ClassStore
		result.Stale = true
		return result, nil
	}
	if updated, listErr := s.store.ListModels(ctx, p.ID); listErr == nil {
		result.Rows = orderedRows(validRows(updated), found)
	} else {
		slog.Warn("providers.catalog.reload_failed", "provider", p.Name, "provider_id", p.ID, "error", listErr)
	}
	return result, nil
}

// ClassStore is the failure class for a catalogue write (not a discovery) failure.
const ClassStore = "store"

// EnsureBundled seeds the bundled snapshot for a provider and returns its rows.
// It performs no network access, so gateway-wide listings can call it per
// provider without a refresh storm.
func (s *Service) EnsureBundled(ctx context.Context, p *store.LLMProviderData) ([]store.LLMModel, error) {
	existing, err := s.store.ListModels(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if _, err := s.seedBundled(ctx, p, existing); err != nil {
		slog.Warn("providers.catalog.seed_failed", "provider", p.Name, "provider_id", p.ID, "error", err)
		return orderedRows(validRows(existing), discovery.Bundled(p.ProviderType)), nil
	}
	rows, err := s.store.ListModels(ctx, p.ID)
	if err != nil {
		return orderedRows(validRows(existing), discovery.Bundled(p.ProviderType)), nil
	}
	return orderedRows(validRows(rows), discovery.Bundled(p.ProviderType)), nil
}

// discover runs the provider's discovery implementation.
func (s *Service) discover(ctx context.Context, p *store.LLMProviderData, ref discovery.ProviderRef) ([]discovery.ModelInfo, error) {
	if s.reg == nil {
		return nil, discovery.Failed(discovery.ClassUnsupported, errNoDiscovery)
	}
	return s.reg.Discover(ctx, ref)
}

// apply writes a merge plan: upserts first, then the membership disables. A
// row is never deleted — disabling keeps every persisted model id resolvable.
func (s *Service) apply(ctx context.Context, p *store.LLMProviderData, plan MergePlan) error {
	if len(plan.Upserts) == 0 && len(plan.Disable) == 0 {
		return nil
	}
	if err := s.store.UpsertModels(ctx, p.ID, plan.Upserts); err != nil {
		return err
	}
	for _, modelID := range plan.Disable {
		if err := s.store.SetModelEnabled(ctx, p.ID, modelID, false); err != nil {
			slog.Warn("providers.catalog.disable_failed", "provider", p.Name, "model", modelID, "error", err)
		}
	}
	return nil
}

// seedBundled upserts the shipped snapshot for the provider's type.
//
// Rules (idempotent by construction — the upsert key is (provider_id, model_id)):
//   - a model the operator owns (source='operator') is never touched;
//   - a row discovered from the upstream (source='discovered') is fresher than the
//     snapshot and is never downgraded;
//   - an existing bundled row is refreshed with snapshot metadata while keeping
//     its provenance (fetched_at, static_fingerprint, authoritative) and the
//     operator's enabled flag.
func (s *Service) seedBundled(ctx context.Context, p *store.LLMProviderData, existing []store.LLMModel) ([]store.LLMModel, error) {
	bundled := discovery.Bundled(p.ProviderType)
	if len(bundled) == 0 {
		return existing, nil
	}
	byID := make(map[string]store.LLMModel, len(existing))
	for _, row := range existing {
		byID[row.ModelID] = row
	}
	upserts := make([]store.LLMModel, 0, len(bundled))
	for _, info := range bundled {
		if row, ok := byID[info.ID]; ok {
			if row.Source != store.ModelSourceBundled {
				continue
			}
			candidate := row
			ApplyInfo(&candidate, info)
			candidate.Source = store.ModelSourceBundled
			if SameUpsertPayload(row, candidate) {
				continue
			}
			upserts = append(upserts, candidate)
			continue
		}
		upserts = append(upserts, NewRow(info, MergeOptions{Source: store.ModelSourceBundled}))
	}
	if len(upserts) == 0 {
		return existing, nil
	}
	if err := s.store.UpsertModels(ctx, p.ID, upserts); err != nil {
		return existing, err
	}
	return existing, nil
}

// allowAttempt enforces the per-provider in-process fetch interval.
func (s *Service) allowAttempt(providerID uuid.UUID, interval time.Duration, now time.Time) bool {
	if interval <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAttempt[providerID]; ok && now.Sub(last) < interval {
		return false
	}
	s.lastAttempt[providerID] = now
	return true
}

// AuthoritativeFromSettings reads settings.discovered_models_authoritative
// (default false: discovery only fills gaps).
func AuthoritativeFromSettings(settings json.RawMessage) bool {
	if len(settings) == 0 {
		return false
	}
	var s struct {
		Authoritative *bool `json:"discovered_models_authoritative"`
	}
	if err := json.Unmarshal(settings, &s); err != nil || s.Authoritative == nil {
		return false
	}
	return *s.Authoritative
}

// Fingerprint is the static_fingerprint rule: a hash of the provider base URL,
// its wire_api and the operator's discovery-relevant overrides (the authority
// flag and the model ids the operator owns/pinned). A cached row set is reused
// only when it carries the same fingerprint, so changing api_base — or pinning
// a model — forces the next list call to re-fetch.
func Fingerprint(baseURL, wireAPI string, authoritative bool, operatorModelIDs []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(wireAPI))
	b.WriteByte('\n')
	if authoritative {
		b.WriteString("authoritative")
	} else {
		b.WriteString("gap-fill")
	}
	b.WriteByte('\n')
	pinned := append([]string(nil), operatorModelIDs...)
	sort.Strings(pinned)
	for _, id := range pinned {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// OperatorModelIDs returns the sorted model ids the operator owns.
func OperatorModelIDs(rows []store.LLMModel) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Source == store.ModelSourceOperator {
			out = append(out, row.ModelID)
		}
	}
	sort.Strings(out)
	return out
}

// cacheState describes how the cached rows relate to the fingerprint and TTL of
// the current provider configuration.
type cacheState struct {
	// tracked is true when at least one row carries a fingerprint, i.e. a
	// discovery has written provenance for this provider before.
	tracked bool
	// matches is true when every tracked row carries the wanted fingerprint.
	matches bool
	// expired is true when the newest fetched_at is older than the TTL.
	expired bool
}

// fresh reports that the cached set belongs to this configuration and is young
// enough to reuse.
func (c cacheState) fresh() bool { return c.tracked && c.matches && !c.expired }

// cacheStateOf inspects the rows against the wanted fingerprint.
func cacheStateOf(rows []store.LLMModel, fingerprint string, ttl time.Duration, now time.Time) cacheState {
	state := cacheState{matches: true}
	var latest time.Time
	for _, row := range rows {
		if row.StaticFingerprint == nil || *row.StaticFingerprint == "" {
			continue
		}
		state.tracked = true
		if *row.StaticFingerprint != fingerprint {
			state.matches = false
		}
		if row.FetchedAt != nil && row.FetchedAt.After(latest) {
			latest = *row.FetchedAt
		}
	}
	if !state.tracked {
		return state
	}
	if ttl > 0 && now.Sub(latest) > ttl {
		state.expired = true
	}
	return state
}

// validRows keeps the rows a caller may serve: model rows scoped to the
// provider, enabled only (a disabled row stays in the database so persisted ids
// survive, but it is not part of the catalogue).
func validRows(rows []store.LLMModel) []store.LLMModel {
	out := make([]store.LLMModel, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		out = append(out, row)
	}
	return out
}

// orderedRows puts the discovery result's order first (the upstream's order, or
// the snapshot's), then any remaining row by model id. The order is stable
// across calls so the picker does not shuffle.
func orderedRows(rows []store.LLMModel, order []discovery.ModelInfo) []store.LLMModel {
	if len(rows) == 0 || len(order) == 0 {
		return sortRows(rows)
	}
	position := make(map[string]int, len(order))
	for i, info := range order {
		if _, ok := position[info.ID]; !ok {
			position[info.ID] = i
		}
	}
	sorted := sortRows(rows)
	known := make([]store.LLMModel, 0, len(sorted))
	var rest []store.LLMModel
	for _, row := range sorted {
		if _, ok := position[row.ModelID]; ok {
			known = append(known, row)
			continue
		}
		rest = append(rest, row)
	}
	sort.SliceStable(known, func(i, j int) bool {
		return position[known[i].ModelID] < position[known[j].ModelID]
	})
	return append(known, rest...)
}

// sortRows orders rows by model id (the store's own order, made explicit).
func sortRows(rows []store.LLMModel) []store.LLMModel {
	out := append([]store.LLMModel(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}
