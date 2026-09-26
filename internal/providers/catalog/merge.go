package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// errNoDiscovery is returned when a gateway has no discovery registry (lite
// editions and unit tests): seeding and cached reads still work.
var errNoDiscovery = errors.New("model discovery is not configured")

// MergeOptions describes what a discovery result is allowed to do to the
// catalogue.
type MergeOptions struct {
	// Source is the value written to llm_models.source for rows this merge
	// creates (bundled for a static snapshot, discovered for a live listing).
	Source string
	// Authoritative: when true, a successful discovery replaces the bundled
	// membership — bundled rows the upstream no longer lists are disabled (never
	// deleted). When false, discovery only fills gaps and existing rows win.
	Authoritative bool
	// TrackProvenance records static_fingerprint/fetched_at on the rows. Static
	// snapshots do not track it: their content is the snapshot itself.
	TrackProvenance bool
	Fingerprint     string
	FetchedAt       time.Time
}

// MergePlan is the change set a merge produces.
type MergePlan struct {
	// Upserts are rows to insert or refresh (never operator-owned).
	Upserts []store.LLMModel
	// Disable lists model ids to switch off (membership replacement). Rows are
	// disabled rather than deleted so persisted ids stay resolvable.
	Disable []string
}

// PlanMerge applies the merge rule to one discovery result.
//
//	operator rows        never touched, in either mode
//	authoritative=true   the upstream list wins: known rows are refreshed,
//	                     unknown rows are inserted, rows the upstream dropped are
//	                     disabled
//	authoritative=false  gap fill only: unknown rows are inserted, every existing
//	                     row keeps its metadata
func PlanMerge(existing []store.LLMModel, found []discovery.ModelInfo, opts MergeOptions) MergePlan {
	byID := make(map[string]store.LLMModel, len(existing))
	for _, row := range existing {
		byID[row.ModelID] = row
	}
	foundIDs := make(map[string]bool, len(found))
	var plan MergePlan
	for _, info := range found {
		if info.ID == "" || foundIDs[info.ID] {
			continue
		}
		foundIDs[info.ID] = true
		row, ok := byID[info.ID]
		if !ok {
			plan.Upserts = append(plan.Upserts, NewRow(info, opts))
			continue
		}
		if row.Source == store.ModelSourceOperator {
			continue
		}
		candidate := row
		if !opts.Authoritative {
			// Gap fill: the upstream may only tell us that the cached set is
			// current — the row's metadata stays, its provenance is refreshed so
			// the fingerprint check converges instead of re-fetching forever.
			applyProvenance(&candidate, opts)
		} else {
			ApplyInfo(&candidate, info)
			candidate.Source = opts.Source
			candidate.Authoritative = opts.Authoritative
			applyProvenance(&candidate, opts)
		}
		if SameUpsertPayload(row, candidate) {
			continue
		}
		plan.Upserts = append(plan.Upserts, candidate)
	}
	if !opts.Authoritative {
		return plan
	}
	for _, row := range existing {
		if row.Source == store.ModelSourceOperator || !row.Enabled {
			continue
		}
		if foundIDs[row.ModelID] {
			continue
		}
		plan.Disable = append(plan.Disable, row.ModelID)
	}
	return plan
}

// NewRow materializes a discovery entry as a new catalogue row.
func NewRow(info discovery.ModelInfo, opts MergeOptions) store.LLMModel {
	row := store.LLMModel{
		ModelID:       info.ID,
		Source:        opts.Source,
		Authoritative: opts.Authoritative,
		Enabled:       true,
	}
	ApplyInfo(&row, info)
	applyProvenance(&row, opts)
	return row
}

// ApplyInfo overlays the metadata an inspection learned onto a row. Unknown
// fields (nil pointer / empty string) leave the row's value untouched, so a
// later discovery that knows less never erases what an earlier one knew.
func ApplyInfo(row *store.LLMModel, info discovery.ModelInfo) {
	if info.DisplayName != "" {
		row.DisplayName = &info.DisplayName
	}
	if info.ContextWindow != nil {
		row.ContextWindow = info.ContextWindow
	}
	if info.MaxTokens != nil {
		row.MaxTokens = info.MaxTokens
	}
	if info.Tokenizer != "" {
		row.Tokenizer = &info.Tokenizer
	}
	if len(info.Capabilities) > 0 {
		if raw, err := json.Marshal(info.Capabilities); err == nil {
			row.Capabilities = raw
		}
	}
	if len(info.Modalities) > 0 {
		if raw, err := json.Marshal(info.Modalities); err == nil {
			row.Modalities = raw
		}
	}
}

func applyProvenance(row *store.LLMModel, opts MergeOptions) {
	if !opts.TrackProvenance {
		return
	}
	if opts.Fingerprint != "" {
		fp := opts.Fingerprint
		row.StaticFingerprint = &fp
	}
	if !opts.FetchedAt.IsZero() {
		at := opts.FetchedAt
		row.FetchedAt = &at
	}
}

// SameUpsertPayload reports whether writing candidate over current would change
// nothing — the comparison covers exactly the columns the upsert statement
// writes (enabled is excluded: the upsert preserves the operator's flag).
func SameUpsertPayload(current, candidate store.LLMModel) bool {
	return eqStrPtr(current.DisplayName, candidate.DisplayName) &&
		eqStrPtr(current.WireAPI, candidate.WireAPI) &&
		eqIntPtr(current.ContextWindow, candidate.ContextWindow) &&
		eqIntPtr(current.MaxTokens, candidate.MaxTokens) &&
		eqIntPtr(current.MaxContextWindow, candidate.MaxContextWindow) &&
		eqFloatPtr(current.CostInput, candidate.CostInput) &&
		eqFloatPtr(current.CostOutput, candidate.CostOutput) &&
		eqFloatPtr(current.CostCacheRead, candidate.CostCacheRead) &&
		eqFloatPtr(current.CostCacheWrite, candidate.CostCacheWrite) &&
		bytes.Equal(current.Modalities, candidate.Modalities) &&
		bytes.Equal(current.Capabilities, candidate.Capabilities) &&
		bytes.Equal(current.Reasoning, candidate.Reasoning) &&
		eqStrPtr(current.Tokenizer, candidate.Tokenizer) &&
		bytes.Equal(current.Compat, candidate.Compat) &&
		current.Source == candidate.Source &&
		current.Authoritative == candidate.Authoritative &&
		eqStrPtr(current.StaticFingerprint, candidate.StaticFingerprint) &&
		eqTimePtr(current.FetchedAt, candidate.FetchedAt)
}

func eqStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
