package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/providers/catalog"
	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/providers/wire"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// apiCapabilityFlags is the capability object every surface reads: the
// resolution the request path applies (provider declaration overlaid with the
// catalogue row), reduced to the four flags a picker can act on.
//
// A flag is true only because a declaration says so — the live transport's
// Capabilities(), the wire descriptor's tool/stream declaration, or the
// catalogue row's per-model override. Nothing here is inferred from the provider
// name, the base URL or the model id.
type apiCapabilityFlags struct {
	ToolCalling     bool `json:"tool_calling"`
	Vision          bool `json:"vision"`
	StreamWithTools bool `json:"stream_with_tools"`
	CacheControl    bool `json:"cache_control"`
}

// apiCapabilityModel is one model of the capability DTO.
//
// ID is "<provider>/<model-id>" — the same identity GET /v1/models and
// chat.send use, so a picker never has to join two shapes.
type apiCapabilityModel struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	ContextWindow *int   `json:"context_window,omitempty"`
	MaxTokens     *int   `json:"max_tokens,omitempty"`
	// ThinkingLevels/DefaultThinkingLevel come from the reasoning capability the
	// request path resolves for this model id; omitted when the model declares
	// none.
	ThinkingLevels       []string           `json:"thinking_levels,omitempty"`
	DefaultThinkingLevel string             `json:"default_thinking_level,omitempty"`
	Capabilities         apiCapabilityFlags `json:"capabilities"`
	Cost                 *modelCostDTO      `json:"cost,omitempty"`
	// Stale marks a model whose provider catalogue is known (fingerprint changed)
	// or presumed (TTL expired) not to describe the current upstream.
	Stale bool `json:"stale"`
}

// apiProviderCapability is the capability DTO of one provider: the single shape
// the web UI, the desktop UI and the CLI build a picker from.
//
// It carries no transport detail by construction — api_base, exec_path, the
// credential, the settings blob and the compat object are not representable
// here, so no surface can render them by accident.
type apiProviderCapability struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	Label      string `json:"label"`
	WireAPI    string `json:"wire_api"`
	AuthKind   string `json:"auth_kind"`
	// ModelSource is where the listed models came from: the bundled snapshot or a
	// provider discovery.
	ModelSource string `json:"model_source"`
	// DefaultModelID is "<provider>/<model>" for the brand's default model, when
	// the catalogue actually holds it.
	DefaultModelID string               `json:"default_model_id,omitempty"`
	Models         []apiCapabilityModel `json:"models"`
	// Stale mirrors the provider's catalogue cache state (see catalog.CacheState);
	// each model repeats it so a row is self-describing.
	Stale bool `json:"stale"`
	// LastRefreshedAt is the newest catalogue fetch, when one happened.
	LastRefreshedAt *time.Time `json:"last_refreshed_at,omitempty"`
}

type apiProviderCapabilitiesResponse struct {
	Providers []apiProviderCapability `json:"providers"`
}

// capabilityCacheOnly is the catalogue read the capability DTO performs: seeded
// and stored rows, never a network fetch. A retry belongs to
// GET /v1/providers/{id}/models?refresh=true, which also reports the classified
// discovery error.
var capabilityCacheOnly = catalog.Options{Fetch: false}

// handleListProviderCapabilities serves the capability DTO every UI surface
// builds its provider/model picker from.
//
//	GET /v1/providers/capabilities[?id=<provider id or name>]
//
// It is a cache read (see capabilityCacheOnly) and omits disabled providers, so
// it never lists a provider the request path could not run.
func (h *ProvidersHandler) handleListProviderCapabilities(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	ctx := r.Context()

	allProviders, err := h.store.ListProviders(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgFailedToList, "providers")})
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("id"))
	out := make([]apiProviderCapability, 0, len(allProviders))
	for i := range allProviders {
		p := &allProviders[i]
		if !p.Enabled {
			continue
		}
		if ref != "" && p.Name != ref && !strings.EqualFold(p.ID.String(), ref) {
			continue
		}
		out = append(out, h.providerCapabilityDTO(ctx, p))
	}
	writeJSON(w, http.StatusOK, apiProviderCapabilitiesResponse{Providers: out})
}

// providerCapabilityDTO assembles one provider's DTO: the catalogue rows (cache
// read) plus the declarations the request path resolves.
func (h *ProvidersHandler) providerCapabilityDTO(ctx context.Context, p *store.LLMProviderData) apiProviderCapability {
	dto := apiProviderCapability{
		ID:         p.Name,
		ProviderID: p.ID.String(),
		Label:      providerLabel(p),
		WireAPI:    p.WireAPI,
		AuthKind:   p.AuthKind,
		Models:     []apiCapabilityModel{},
	}
	if h.modelCatalog == nil {
		return dto
	}

	ref := h.discoveryRef(p)
	result, err := h.modelCatalog.Sync(ctx, p, ref, capabilityCacheOnly)
	if err != nil {
		// One unreadable provider must not hide the others: it stays listed with
		// no models, and the failure is logged.
		slog.Warn("providers.capabilities_store_failed", "provider", p.Name, "provider_id", p.ID, "error", err)
		return dto
	}
	if state, stateErr := h.modelCatalog.CacheState(ctx, p, ref); stateErr == nil {
		dto.Stale = state.Stale()
		dto.LastRefreshedAt = state.Fetched
	} else {
		slog.Warn("providers.capabilities_cache_state_failed", "provider", p.Name, "provider_id", p.ID, "error", stateErr)
	}

	base := h.baseCapabilitiesFor(p)
	discovered := false
	for _, row := range result.Rows {
		if row.Source == store.ModelSourceDiscovered {
			discovered = true
		}
		dto.Models = append(dto.Models, h.capabilityModelDTO(ctx, p, row, base, dto.Stale))
	}
	dto.ModelSource = modelSource(discovered)
	dto.DefaultModelID = defaultModelID(p, result.Rows)
	return dto
}

// capabilityModelDTO maps one catalogue row to the DTO, resolving capabilities
// through the same resolver the request path uses, so the UI cannot disagree
// with what will actually run.
func (h *ProvidersHandler) capabilityModelDTO(ctx context.Context, p *store.LLMProviderData, row store.LLMModel, base providers.ProviderCapabilities, stale bool) apiCapabilityModel {
	resolution := providers.ResolveModelCapabilities(base, declaredModelCapabilities, p.Name, p.ProviderType, row.ModelID)

	model := apiCapabilityModel{
		ID:    p.Name + "/" + row.ModelID,
		Label: modelLabel(row.ModelID, row.DisplayName),
		Capabilities: apiCapabilityFlags{
			ToolCalling:     resolution.Capabilities.ToolCalling,
			Vision:          resolution.Capabilities.Vision,
			StreamWithTools: resolution.Capabilities.StreamWithTools,
			CacheControl:    resolution.Capabilities.CacheControl,
		},
		Cost:  h.modelCost(ctx, p, row),
		Stale: stale,
	}

	// The catalogue row owns the window; a row that declares none inherits the
	// clamp the request path would apply to its budget.
	switch {
	case row.ContextWindow != nil && *row.ContextWindow > 0:
		model.ContextWindow = row.ContextWindow
	case resolution.ContextWindowClamp > 0:
		window := resolution.ContextWindowClamp
		model.ContextWindow = &window
	}
	if row.MaxTokens != nil && *row.MaxTokens > 0 {
		model.MaxTokens = row.MaxTokens
	}
	if reasoning := providers.LookupReasoningCapability(row.ModelID); reasoning != nil {
		model.ThinkingLevels = reasoning.Levels
		model.DefaultThinkingLevel = reasoning.DefaultEffort
	}
	return model
}

// baseCapabilitiesFor is the provider's own declaration: the registered
// transport's Capabilities() when it is live, otherwise the wire descriptor's
// declared tool/stream shape. No provider name, base URL or model id takes part.
func (h *ProvidersHandler) baseCapabilitiesFor(p *store.LLMProviderData) providers.ProviderCapabilities {
	if h.providerReg != nil {
		if prov, err := h.providerReg.GetForTenant(p.TenantID, p.Name); err == nil {
			if aware, ok := prov.(providers.CapabilitiesAware); ok {
				return aware.Capabilities()
			}
		}
	}
	return descriptorCapabilities(p.WireAPI)
}

// descriptorCapabilities is the wire family's declared capability shape, used
// when no live transport is registered for the row (a provider registered but
// not yet rebuilt, or a build whose registry is unwired).
func descriptorCapabilities(wireAPI string) providers.ProviderCapabilities {
	desc, ok := wire.Lookup(wire.API(wireAPI))
	if !ok {
		return providers.ProviderCapabilities{}
	}
	return providers.ProviderCapabilities{
		Streaming:       desc.SupportsStream,
		ToolCalling:     desc.SupportsTools,
		StreamWithTools: desc.SupportsStreamWithTools,
		TokenizerID:     desc.TokenizerID,
	}
}

// declaredModelCapabilities resolves a catalogue row's own declaration through
// the shipped snapshot — the same lookup the request path installs — so the DTO
// and the pipeline cannot disagree about one model.
func declaredModelCapabilities(_, providerType, model string) (providers.ModelCapabilityOverride, bool) {
	return discovery.BundledModelCapabilities(providerType, model)
}

// modelSource reports where the served list came from. Rows say so themselves: a
// discovery that returned nothing new leaves the bundled source in place.
func modelSource(discovered bool) string {
	if discovered {
		return store.ModelSourceDiscovered
	}
	return store.ModelSourceBundled
}

// defaultModelID resolves "<provider>/<default model>" from the brand's declared
// default, falling back to the wire family's, and only when the catalogue holds
// that model: a picker must not be handed an identity it cannot select.
func defaultModelID(p *store.LLMProviderData, rows []store.LLMModel) string {
	candidates := make([]string, 0, 2)
	if brand, ok := wire.BrandFor(p.ProviderType); ok && brand.Model != "" {
		candidates = append(candidates, brand.Model)
	}
	if desc, ok := wire.Lookup(wire.API(p.WireAPI)); ok && desc.DefaultModel != "" {
		candidates = append(candidates, desc.DefaultModel)
	}
	for _, candidate := range candidates {
		for _, row := range rows {
			if row.ModelID == candidate {
				return p.Name + "/" + candidate
			}
		}
	}
	return ""
}

// providerLabel is the human label of a provider row: its display name when the
// operator set one, its name otherwise.
func providerLabel(p *store.LLMProviderData) string {
	if label := strings.TrimSpace(p.DisplayName); label != "" {
		return label
	}
	return p.Name
}
