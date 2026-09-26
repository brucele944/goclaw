package discovery

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// ollamaDiscovery lists a local/self-hosted Ollama (or Ollama Cloud) instance
// through its native API: GET /api/tags for the inventory, then POST /api/show
// per model for the native context length. This is today's behaviour of the
// provider models endpoint, kept verbatim so the picker does not change.
type ollamaDiscovery struct {
	guard URLGuard
}

// ollamaShowTimeout bounds a single /api/show lookup. Context length is optional
// metadata: a slow or unreachable show endpoint must not fail the inventory.
const ollamaShowTimeout = 10 * time.Second

func (d *ollamaDiscovery) Type() string { return TypeOllama }

// ollamaBase strips the /v1 suffix the gateway adds to Ollama api_base values
// (issue #654 normalization): /api/tags and /api/show live at the root.
func ollamaBase(apiBase string) string {
	return strings.TrimRight(strings.TrimSuffix(strings.TrimRight(apiBase, "/"), "/v1"), "/")
}

func (d *ollamaDiscovery) List(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	base := trimBase(p.BaseURL)
	if base == "" {
		base = "http://localhost:11434"
	}
	rawURL := config.DockerLocalhost(ollamaBase(base) + "/api/tags")
	if err := checkURL(d.guard, rawURL, p.ProviderType); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if p.APIKey != "" {
		headers["Authorization"] = "Bearer " + p.APIKey
	}
	var payload struct {
		Models []struct {
			Name    string `json:"name"`
			Details struct {
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := doJSON(ctx, http.MethodGet, rawURL, headers, nil, "ollama /api/tags", &payload); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	for _, m := range payload.Models {
		if strings.TrimSpace(m.Name) == "" {
			continue
		}
		info := ModelInfo{ID: m.Name, DisplayName: ollamaDisplayName(m.Name, m.Details.Family, m.Details.ParameterSize, m.Details.QuantizationLevel)}
		// Context length is a per-model /api/show answer; leave it NULL when the
		// endpoint cannot answer rather than guessing a default.
		if ctx.Err() == nil {
			info.ContextWindow = d.modelContext(ctx, base, p, m.Name)
		}
		models = append(models, info)
	}
	return models, nil
}

// modelContext resolves a model's native context length, or nil when the
// endpoint does not answer (the caller leaves the column NULL).
func (d *ollamaDiscovery) modelContext(ctx context.Context, base string, p ProviderRef, model string) *int {
	showCtx, cancel := context.WithTimeout(ctx, ollamaShowTimeout)
	defer cancel()
	n, err := providers.FetchOllamaModelContextOrError(showCtx, base, model, p.APIKey)
	if err != nil || n <= 0 {
		slog.Debug("providers.discovery.ollama.context_unavailable", "provider", p.Name, "model", model, "error", err)
		return nil
	}
	return intPtr(n)
}

// ollamaDisplayName builds the human-readable label from the model details,
// omitting missing parts ("gemma4 8.0B Q4_K_M"). Falls back to the raw name.
func ollamaDisplayName(name, family, parameterSize, quantization string) string {
	parts := make([]string, 0, 3)
	for _, part := range []string{family, parameterSize, quantization} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return name
	}
	return strings.Join(parts, " ")
}
