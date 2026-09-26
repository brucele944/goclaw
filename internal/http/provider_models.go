package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/providers/catalog"
	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/providers/wire"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ModelInfo is a normalized model entry returned by the list-models endpoint.
type ModelInfo struct {
	ID        string                         `json:"id"`
	Name      string                         `json:"name,omitempty"`
	Reasoning *providers.ReasoningCapability `json:"reasoning,omitempty"`
}

// ProviderModelsResponse is the body of GET /v1/providers/{id}/models.
//
// stale/error are the diagnostics that replace the old silent `{"models": []}`:
// a listing failure keeps serving the cached models, marks them stale and names
// the failure class so the UI can offer a retry instead of showing "no models".
type ProviderModelsResponse struct {
	Models            []ModelInfo                    `json:"models"`
	ReasoningDefaults *store.ProviderReasoningConfig `json:"reasoning_defaults,omitempty"`
	Stale             bool                           `json:"stale"`
	Error             string                         `json:"error,omitempty"`
	ErrorClass        string                         `json:"error_class,omitempty"`
	// Fetched reports whether this call actually reached the upstream.
	Fetched bool `json:"fetched"`
}

// handleListProviderModels lists a provider's models.
//
// The response is the provider's catalogue (llm_models rows, seeded from the
// bundled snapshot and refreshed through discovery when the fingerprint cache
// says it is due) — not a direct proxy of the upstream, so a listing outage
// degrades to the last good set instead of an empty list.
//
//	GET /v1/providers/{id}/models[?refresh=true]
func (h *ProvidersHandler) handleListProviderModels(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgInvalidID, "provider")})
		return
	}

	p, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": i18n.T(locale, i18n.MsgNotFound, "provider", id.String())})
		return
	}

	if modelsRequireAPIKey(p.ProviderType) && p.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T(locale, i18n.MsgRequired, "API key")})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.providerModelsTimeout(r.Context(), p))
	defer cancel()

	result, err := h.modelCatalog.Sync(ctx, p, h.discoveryRef(p), catalog.Options{
		Fetch:   true,
		Refresh: strings.EqualFold(r.URL.Query().Get("refresh"), "true"),
	})
	if err != nil {
		slog.Error("providers.models.store", "provider", p.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgProviderModelsFailed, p.Name)})
		return
	}

	models := providerModelInfos(p.ProviderType, result)
	resp := ProviderModelsResponse{
		Models:            models,
		ReasoningDefaults: reasoningDefaultsForModels(p.Settings, models),
		Stale:             result.Stale,
		Fetched:           result.Fetched,
	}
	if result.Err != nil {
		resp.Error = i18n.T(locale, i18n.MsgProviderDiscoveryFailed, p.Name, result.Err.Error())
		resp.ErrorClass = result.ErrorClass
		slog.Warn("providers.models.discovery_failed", "provider", p.Name, "class", result.ErrorClass, "error", result.Err)
	}
	writeJSON(w, http.StatusOK, resp)
}

// providerModelInfos maps a catalogue result to the wire DTO: catalogue rows when
// the store holds them, the inspection result otherwise (a gateway whose store
// is read-only or unwired still answers with the bundled snapshot).
func providerModelInfos(providerType string, result catalog.Result) []ModelInfo {
	annotate := annotatesReasoning(providerType)
	if len(result.Rows) > 0 {
		out := make([]ModelInfo, 0, len(result.Rows))
		for _, row := range result.Rows {
			out = append(out, ModelInfo{ID: row.ModelID, Name: modelLabel(row.ModelID, row.DisplayName)})
		}
		if annotate {
			return withReasoningCapabilities(out)
		}
		return out
	}
	out := make([]ModelInfo, 0, len(result.Models))
	for _, info := range result.Models {
		out = append(out, ModelInfo{ID: info.ID, Name: modelLabel(info.ID, displayNamePtr(info.DisplayName))})
	}
	if annotate {
		return withReasoningCapabilities(out)
	}
	return out
}

// modelLabel falls back to the model id when no display name is known.
func modelLabel(modelID string, displayName *string) string {
	if displayName == nil || strings.TrimSpace(*displayName) == "" {
		return modelID
	}
	return *displayName
}

// displayNamePtr turns an empty display name into nil (unknown) rather than an
// empty label.
func displayNamePtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// annotatesReasoning preserves the pre-catalog behaviour exactly: the CLI, ACP
// and Ollama listings never carried reasoning capability metadata (ChatGPT OAuth
// models carry it, and re-annotating them is idempotent).
func annotatesReasoning(providerType string) bool {
	switch providerType {
	case store.ProviderClaudeCLI, store.ProviderACP, store.ProviderOllama, store.ProviderOllamaCloud:
		return false
	default:
		return true
	}
}

// modelsRequireAPIKey reports whether the listing path needs a credential.
// The subprocess, OAuth and Ollama transports do not (legacy behaviour).
func modelsRequireAPIKey(providerType string) bool {
	switch providerType {
	case store.ProviderClaudeCLI, store.ProviderChatGPTOAuth, store.ProviderACP,
		store.ProviderOllama, store.ProviderOllamaCloud:
		return false
	default:
		return true
	}
}

// discoveryRef reduces a provider row to the transport-facing discovery input.
//
// The base URL and identity headers reuse the pre-catalog resolution
// (openAIModelsAPIBase / openAIModelsExtraHeaders) so the outbound request shape
// of every existing provider is unchanged; the Anthropic and Gemini native
// listings resolve their own default base inside discovery.
func (h *ProvidersHandler) discoveryRef(p *store.LLMProviderData) discovery.ProviderRef {
	base := h.resolveAPIBase(p)
	switch p.ProviderType {
	case store.ProviderAnthropicNative, store.ProviderGeminiNative:
		// Native listing APIs: a custom api_base is honoured as-is, the vendor
		// default belongs to the discovery implementation.
	default:
		base = openAIModelsAPIBase(p.ProviderType, base)
	}
	return discovery.ProviderRef{
		ID:           p.ID,
		Name:         p.Name,
		ProviderType: p.ProviderType,
		WireAPI:      p.WireAPI,
		BaseURL:      base,
		APIKey:       p.APIKey,
		ExtraHeaders: openAIModelsExtraHeaders(p.ProviderType),
		Settings:     p.Settings,
	}
}

// providerModelsTimeout is the outbound deadline for a listing call: the
// provider's own settings.timeout_sec when declared, else the tenant's
// providers.request_timeout_sec.
func (h *ProvidersHandler) providerModelsTimeout(ctx context.Context, p *store.LLMProviderData) time.Duration {
	if d := wire.TimeoutFromSettings(p.Settings); d > 0 {
		return d
	}
	return time.Duration(loadProviderRequestTimeoutSec(ctx, h.sysConfigStore)) * time.Second
}

// openAIModelsAPIBase resolves the base URL of an OpenAI-shaped /models call.
func openAIModelsAPIBase(providerType, apiBase string) string {
	base := strings.TrimRight(apiBase, "/")
	if base != "" {
		return base
	}
	switch providerType {
	case store.ProviderAtlasCloud:
		return store.AtlasCloudDefaultAPIBase
	case store.ProviderKimiCoding:
		return store.KimiCodingDefaultAPIBase
	default:
		return "https://api.openai.com/v1"
	}
}

// openAIModelsExtraHeaders returns the identity headers a vendor requires on its
// /models endpoint (Kimi Coding rejects requests without this exact User-Agent).
func openAIModelsExtraHeaders(providerType string) map[string]string {
	if providerType != store.ProviderKimiCoding {
		return nil
	}
	return map[string]string{
		"User-Agent": store.KimiCodingRequiredUserAgent,
	}
}

func reasoningDefaultsForModels(
	settings []byte,
	models []ModelInfo,
) *store.ProviderReasoningConfig {
	if len(models) == 0 {
		return nil
	}
	for _, model := range models {
		if model.Reasoning != nil {
			return store.ParseProviderReasoningConfig(settings)
		}
	}
	return nil
}

func withReasoningCapabilities(models []ModelInfo) []ModelInfo {
	result := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		next := model
		next.Reasoning = providers.LookupReasoningCapability(model.ID)
		result = append(result, next)
	}
	return result
}
