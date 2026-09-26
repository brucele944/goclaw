package discovery

import (
	"context"
	"net/http"
	"strings"
)

// openAIModelsList lists models from a HTTP `/models` endpoint.
//
// One implementation covers every provider whose upstream exposes a model list;
// the declared wire_api selects the auth header and the response shape, which is
// the only part that differs between vendors:
//
//	openai-completions / openai-responses  GET {base}/models, Bearer, data[].id
//	anthropic-messages                     GET {base}/models, x-api-key, data[].display_name
//	google-generative-ai                   GET {base}/models?key=…, models[].name
//
// The base URL comes from the resolved api_base (internal/http keeps the legacy
// per-brand default so nothing about the request shape changes for existing
// rows); the implementation's own defaults apply only when the row has none.
type openAIModelsList struct {
	guard URLGuard
}

const (
	anthropicDefaultModelsBase = "https://api.anthropic.com/v1"
	openAIDefaultModelsBase    = "https://api.openai.com/v1"
	// geminiNativeModelsBase is the native (non-OpenAI-compat) listing root.
	geminiNativeModelsBase = "https://generativelanguage.googleapis.com/v1beta"
)

func (d *openAIModelsList) Type() string { return TypeOpenAIModelsList }

func (d *openAIModelsList) List(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	switch p.WireAPI {
	case "anthropic-messages":
		return d.listAnthropic(ctx, p)
	case "google-generative-ai":
		return d.listGemini(ctx, p)
	default:
		return d.listOpenAI(ctx, p)
	}
}

func (d *openAIModelsList) guardURL(rawURL string, p ProviderRef) error {
	return checkURL(d.guard, rawURL, p.ProviderType)
}

func (d *openAIModelsList) listOpenAI(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	base := trimBase(p.BaseURL)
	if base == "" {
		base = openAIDefaultModelsBase
	}
	rawURL := base + "/models"
	if err := d.guardURL(rawURL, p); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if p.APIKey != "" {
		headers["Authorization"] = "Bearer " + p.APIKey
	}
	for k, v := range p.ExtraHeaders {
		headers[k] = v
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doJSON(ctx, http.MethodGet, rawURL, headers, nil, "provider /models", &payload); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		// Legacy behaviour: the label is the model id.
		models = append(models, ModelInfo{ID: m.ID, DisplayName: m.ID})
	}
	return models, nil
}

func (d *openAIModelsList) listAnthropic(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	base := trimBase(p.BaseURL)
	if base == "" {
		base = anthropicDefaultModelsBase
	}
	rawURL := base + "/models"
	if err := d.guardURL(rawURL, p); err != nil {
		return nil, err
	}
	headers := map[string]string{"anthropic-version": "2023-06-01"}
	if p.APIKey != "" {
		headers["x-api-key"] = p.APIKey
	}
	for k, v := range p.ExtraHeaders {
		headers[k] = v
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := doJSON(ctx, http.MethodGet, rawURL, headers, nil, "anthropic /models", &payload); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		models = append(models, ModelInfo{ID: m.ID, DisplayName: m.DisplayName})
	}
	return models, nil
}

// listGemini uses the native generative-language listing (GET /v1beta/models),
// whose response nests the usable id behind a "models/" prefix.
func (d *openAIModelsList) listGemini(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	base := trimBase(p.BaseURL)
	// The brand's api_base is the OpenAI-compat root; the native listing lives at
	// the parent path. A custom base is honoured as-is (proxy setups).
	if base == "" || strings.Contains(base, "generativelanguage.googleapis.com") {
		base = geminiNativeModelsBase
	}
	rawURL := base + "/models"
	if p.APIKey != "" {
		rawURL += "?key=" + p.APIKey
	}
	if err := d.guardURL(rawURL, p); err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := doJSON(ctx, http.MethodGet, rawURL, p.ExtraHeaders, nil, "gemini /models", &payload); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	for _, m := range payload.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if strings.TrimSpace(id) == "" {
			continue
		}
		models = append(models, ModelInfo{ID: id, DisplayName: m.DisplayName})
	}
	return models, nil
}
