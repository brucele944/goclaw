package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	usagepricing "github.com/nextlevelbuilder/goclaw/internal/usage/pricing"
)

// quirksSeedOnce makes the bundled quirk rows visible to operators exactly once
// per process. Failure is logged and retried on the next call, so a transient
// database error cannot permanently hide the table.
var (
	quirksSeedMu sync.Mutex
	quirksSeeded bool
)

// ensureQuirks persists the bundled quirk seeds (idempotent; operator rows are
// never touched). It is best-effort: the resolver always applies the bundled
// seeds from Go, this only makes them inspectable.
func (h *ProvidersHandler) ensureQuirks(ctx context.Context) {
	if h.modelCatalog == nil {
		return
	}
	quirksSeedMu.Lock()
	defer quirksSeedMu.Unlock()
	if quirksSeeded {
		return
	}
	if err := h.modelCatalog.EnsureQuirks(ctx); err != nil {
		return
	}
	quirksSeeded = true
}

// modelsEndpointResponse is the OpenAI-shaped model list body.
type modelsEndpointResponse struct {
	Object string               `json:"object"` // "list"
	Data   []modelsEndpointItem `json:"data"`
}

// modelsEndpointItem is one model of GET /v1/models.
//
// Identity is "<provider>/<model-id>" (provider name, the same slug the capability
// DTO and the CLI use) and the payload is the OpenAI shape plus the metadata the
// picker needs. No transport detail — api_base, exec_path, the credential, the
// provider settings blob — is representable here by construction.
type modelsEndpointItem struct {
	ID            string         `json:"id"`
	Object        string         `json:"object"` // "model"
	Created       int64          `json:"created"`
	OwnedBy       string         `json:"owned_by"`
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	Name          string         `json:"name,omitempty"`
	ContextWindow *int           `json:"context_window,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	Capabilities  map[string]any `json:"capabilities,omitempty"`
	Cost          *modelCostDTO  `json:"cost,omitempty"`
	Source        string         `json:"source"`
}

// modelDetailResponse is the body of GET /v1/models/{provider}/{model}.
type modelDetailResponse struct {
	ID               string          `json:"id"`
	Object           string          `json:"object"` // "model"
	Provider         string          `json:"provider"`
	ProviderID       string          `json:"provider_id"`
	OwnedBy          string          `json:"owned_by"`
	Model            string          `json:"model"`
	Name             string          `json:"name,omitempty"`
	ContextWindow    *int            `json:"context_window,omitempty"`
	MaxContextWindow *int            `json:"max_context_window,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Modalities       []string        `json:"modalities,omitempty"`
	Capabilities     map[string]any  `json:"capabilities,omitempty"`
	Reasoning        json.RawMessage `json:"reasoning,omitempty"`
	Tokenizer        string          `json:"tokenizer,omitempty"`
	// Compat is a summary, never the compat object itself: a dialect's extra body
	// is transport detail and must not leak through a models endpoint.
	Compat        *compatSummary `json:"compat,omitempty"`
	Cost          *modelCostDTO  `json:"cost,omitempty"`
	Source        string         `json:"source"`
	Authoritative bool           `json:"authoritative"`
	FetchedAt     *time.Time     `json:"fetched_at,omitempty"`
}

// modelCostDTO is a per-1M-token price set. Source says where it came from:
// "row" (the catalogue row declares it) or "pricing_catalog" (resolved from the
// synced OpenRouter catalog because the row left cost NULL).
type modelCostDTO struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Source     string  `json:"source"`
}

// compatSummary lists the compat keys a row declares, without their values, plus
// the resolved compat traits the runtime would apply for this (provider, model).
type compatSummary struct {
	Keys []string `json:"keys"`
	// Resolved are the trait names of the resolved object (the same Resolve the
	// request path uses). Values are never included.
	Resolved []string `json:"resolved,omitempty"`
}

// compatSecretKeys are compat keys whose values never leave the server: they
// carry credentials or a raw request-body injection.
var compatSecretKeys = map[string]bool{
	"extra_body":    true,
	"extrabody":     true,
	"headers":       true,
	"api_key":       true,
	"apikey":        true,
	"authorization": true,
}

// handleListModels serves the caller's tenant-scoped model catalogue in the
// OpenAI shape so an SDK or the web picker can enumerate what this gateway can
// actually run. Disabled models and disabled providers are not listed.
//
// It never runs discovery: the bundled snapshot is seeded on the way through and
// the cached catalogue is served, so the endpoint stays fast and cannot be used
// as a refresh amplifier.
//
//	GET /v1/models
func (h *ProvidersHandler) handleListModels(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	ctx := r.Context()
	h.ensureQuirks(ctx)

	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		slog.Error("models.list_providers", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgFailedToList, "providers")})
		return
	}

	data := make([]modelsEndpointItem, 0, len(providers))
	for i := range providers {
		p := &providers[i]
		if !p.Enabled {
			continue
		}
		rows, err := h.modelCatalog.EnsureBundled(ctx, p)
		if err != nil {
			slog.Warn("models.list_provider_models", "provider", p.Name, "error", err)
			continue
		}
		for _, row := range rows {
			data = append(data, h.modelsEndpointItem(ctx, p, row))
		}
	}
	writeJSON(w, http.StatusOK, modelsEndpointResponse{Object: "list", Data: data})
}

// handleGetModel serves one catalogue entry by identity.
//
//	GET /v1/models/{provider}/{model...}
//
// {provider} is the provider name (its id is accepted too); {model...} keeps
// multi-segment vendor ids (OpenRouter-style "openai/gpt-5.5") addressable.
func (h *ProvidersHandler) handleGetModel(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	h.ensureQuirks(r.Context())
	providerRef := strings.TrimSpace(r.PathValue("provider"))
	modelID := strings.Trim(strings.TrimSpace(r.PathValue("model")), "/")
	if providerRef == "" || modelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgProviderModelRequired)})
		return
	}

	p, err := h.providerByIdentity(r.Context(), providerRef)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgProviderNotFound, providerRef)})
		return
	}
	if !p.Enabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgProviderNotFound, providerRef)})
		return
	}

	rows, err := h.modelCatalog.EnsureBundled(r.Context(), p)
	if err != nil {
		slog.Error("models.detail_store", "provider", p.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgFailedToList, "models")})
		return
	}
	for _, row := range rows {
		if row.ModelID != modelID {
			continue
		}
		writeJSON(w, http.StatusOK, h.modelDetail(r.Context(), p, row))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgProviderModelNotFound, modelID, providerRef)})
}

// providerByIdentity resolves a provider by id or by name within the caller's
// tenant.
func (h *ProvidersHandler) providerByIdentity(ctx context.Context, ref string) (*store.LLMProviderData, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return h.store.GetProvider(ctx, id)
	}
	return h.store.GetProviderByName(ctx, ref)
}

// modelsEndpointItem builds the OpenAI-shaped entry for one catalogue row.
func (h *ProvidersHandler) modelsEndpointItem(ctx context.Context, p *store.LLMProviderData, row store.LLMModel) modelsEndpointItem {
	return modelsEndpointItem{
		ID:            p.Name + "/" + row.ModelID,
		Object:        "model",
		Created:       row.CreatedAt.Unix(),
		OwnedBy:       p.Name,
		Provider:      p.Name,
		Model:         row.ModelID,
		Name:          modelLabel(row.ModelID, row.DisplayName),
		ContextWindow: row.ContextWindow,
		MaxTokens:     row.MaxTokens,
		Capabilities:  decodeCapabilities(row.Capabilities),
		Cost:          h.modelCost(ctx, p, row),
		Source:        row.Source,
	}
}

// modelDetail builds the detail entry for one catalogue row.
func (h *ProvidersHandler) modelDetail(ctx context.Context, p *store.LLMProviderData, row store.LLMModel) modelDetailResponse {
	detail := modelDetailResponse{
		ID:               p.Name + "/" + row.ModelID,
		Object:           "model",
		Provider:         p.Name,
		ProviderID:       p.ID.String(),
		OwnedBy:          p.Name,
		Model:            row.ModelID,
		Name:             modelLabel(row.ModelID, row.DisplayName),
		ContextWindow:    row.ContextWindow,
		MaxContextWindow: row.MaxContextWindow,
		MaxTokens:        row.MaxTokens,
		Modalities:       decodeModalityList(row.Modalities),
		Capabilities:     decodeCapabilities(row.Capabilities),
		Tokenizer:        derefString(row.Tokenizer),
		Compat:           h.compatSummaryFor(ctx, p, row),
		Cost:             h.modelCost(ctx, p, row),
		Source:           row.Source,
		Authoritative:    row.Authoritative,
		FetchedAt:        row.FetchedAt,
	}
	if len(row.Reasoning) > 0 {
		detail.Reasoning = row.Reasoning
	}
	return detail
}

// modelCost returns the row's cost, falling back to the synced pricing catalog
// for a row whose cost the operator never set. No new pricing source: this is the
// same OpenRouter catalog tracing and usage caps price calls with.
func (h *ProvidersHandler) modelCost(ctx context.Context, p *store.LLMProviderData, row store.LLMModel) *modelCostDTO {
	if row.CostInput != nil || row.CostOutput != nil {
		return &modelCostDTO{
			Input:      derefFloat(row.CostInput),
			Output:     derefFloat(row.CostOutput),
			CacheRead:  derefFloat(row.CostCacheRead),
			CacheWrite: derefFloat(row.CostCacheWrite),
			Source:     "row",
		}
	}
	if h.usageCaps == nil {
		return nil
	}
	resolved, err := h.usageCaps.ResolvePricing(ctx, store.TenantIDFromContext(ctx), p.Name, row.ModelID)
	if err != nil || resolved == nil {
		return nil
	}
	rates, ok := usagepricing.PerMillionFromFields(resolved.Pricing)
	if !ok {
		return nil
	}
	return &modelCostDTO{
		Input:      rates.Input,
		Output:     rates.Output,
		CacheRead:  rates.CacheRead,
		CacheWrite: rates.CacheWrite,
		Source:     "pricing_catalog",
	}
}

// decodeCapabilities decodes the capabilities JSONB for the wire. A malformed
// blob is reported as "unknown" rather than failing the whole listing.
func decodeCapabilities(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func decodeModalityList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// compatSummaryFrom lists the compat keys a row declares, minus the ones whose
// values are transport secrets (an injected request body, credential headers).
func compatSummaryFrom(raw json.RawMessage) *compatSummary {
	if len(raw) == 0 {
		return nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil || len(all) == 0 {
		return nil
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		if compatSecretKeys[strings.ToLower(k)] {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return &compatSummary{Keys: keys}
}

// compatSummaryFor builds the read-only compat summary for one catalogue row: the
// declared key names (secrets stripped) plus the traits of the object the request
// path would actually use. It resolves with the same resolver as the transport,
// including the operator's quirk rows, so what an operator inspects here is what
// runs. Only key names leave the server.
func (h *ProvidersHandler) compatSummaryFor(ctx context.Context, p *store.LLMProviderData, row store.LLMModel) *compatSummary {
	summary := compatSummaryFrom(row.Compat)
	if summary == nil {
		summary = &compatSummary{}
	}
	if h.modelCatalog != nil {
		summary.Resolved = compat.Resolve(
			p.WireAPI,
			"",
			compat.ModelInfo{ID: row.ModelID, Compat: row.Compat},
			compat.Settings{
				ProviderName: p.Name,
				ProviderType: p.ProviderType,
				APIBase:      p.APIBase,
				Quirks:       h.operatorQuirks(ctx, p.WireAPI),
			},
		).SummaryKeys()
	}
	if len(summary.Keys) == 0 && len(summary.Resolved) == 0 {
		return nil
	}
	return summary
}

// operatorQuirks loads the caller's declared quirk rows for a wire API. A store
// failure yields the bundled behaviour (Resolve always applies the seeds), so the
// summary degrades rather than failing the request.
func (h *ProvidersHandler) operatorQuirks(ctx context.Context, wireAPI string) []compat.Quirk {
	if h.store == nil || wireAPI == "" {
		return nil
	}
	rows, err := h.store.ListQuirks(ctx, wireAPI)
	if err != nil {
		slog.Warn("models.quirks", "wire_api", wireAPI, "error", err)
		return nil
	}
	out := make([]compat.Quirk, 0, len(rows))
	for _, row := range rows {
		q := compat.Quirk{WireAPI: row.WireAPI, Compat: row.Compat, Source: row.Source, Enabled: row.Enabled}
		if row.EndpointFamily != nil {
			q.EndpointFamily = *row.EndpointFamily
		}
		if row.ModelPattern != nil {
			q.ModelPattern = *row.ModelPattern
		}
		out = append(out, q)
	}
	return out
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// handleListQuirks serves declared compatibility quirks filtered by wire_api.
//
//	GET /v1/providers/quirks?wire_api=...
func (h *ProvidersHandler) handleListQuirks(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	ctx := r.Context()
	h.ensureQuirks(ctx)

	wireAPI := strings.TrimSpace(r.URL.Query().Get("wire_api"))
	if wireAPI == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgRequired, "wire_api")})
		return
	}

	rows, err := h.store.ListQuirks(ctx, wireAPI)
	if err != nil {
		slog.Error("providers.list_quirks", "wire_api", wireAPI, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgFailedToList, "quirks")})
		return
	}

	data := make([]quirkEndpointItem, 0, len(rows))
	for _, row := range rows {
		data = append(data, quirkEndpointItem{
			ID:             row.ID.String(),
			WireAPI:        row.WireAPI,
			EndpointFamily: row.EndpointFamily,
			ModelPattern:   row.ModelPattern,
			Compat:         redactCompatJSON(row.Compat),
			CompatKeys:     extractCompatKeys(row.Compat),
			Note:           row.Note,
			Source:         row.Source,
			Enabled:        row.Enabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"quirks": data})
}

type quirkEndpointItem struct {
	ID             string          `json:"id"`
	WireAPI        string          `json:"wire_api"`
	EndpointFamily *string         `json:"endpoint_family,omitempty"`
	ModelPattern   *string         `json:"model_pattern,omitempty"`
	Compat         json.RawMessage `json:"compat"`
	CompatKeys     []string        `json:"compat_keys"`
	Note           *string         `json:"note,omitempty"`
	Source         string          `json:"source"`
	Enabled        bool            `json:"enabled"`
}

func redactCompatJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return raw
	}
	for k := range all {
		if compatSecretKeys[strings.ToLower(k)] {
			all[k] = "<redacted>"
		}
	}
	b, err := json.Marshal(all)
	if err != nil {
		return raw
	}
	return b
}

func extractCompatKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
