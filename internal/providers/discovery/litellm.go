package discovery

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// liteLLMDiscovery lists models from a LiteLLM proxy.
//
// LiteLLM exposes /model/info (management API), which carries the token limits
// the picker wants; when that endpoint is unavailable (proxy without the
// management API) it falls back to the OpenAI-compatible /models shape, which
// still lists every configured model id.
type liteLLMDiscovery struct {
	guard URLGuard
}

func (d *liteLLMDiscovery) Type() string { return TypeLiteLLM }

func (d *liteLLMDiscovery) List(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	base := trimBase(p.BaseURL)
	if base == "" {
		return nil, Failed(ClassInvalidURL, fmt.Errorf("provider %s has no api_base for %s discovery", p.Name, TypeLiteLLM))
	}
	rawURL := base + "/model/info"
	if err := checkURL(d.guard, rawURL, p.ProviderType); err != nil {
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
			ModelName string `json:"model_name"`
			ModelInfo struct {
				MaxInputTokens  *int `json:"max_input_tokens"`
				MaxOutputTokens *int `json:"max_output_tokens"`
			} `json:"model_info"`
		} `json:"data"`
	}
	err := doJSON(ctx, http.MethodGet, rawURL, headers, nil, "litellm /model/info", &payload)
	if err != nil {
		if ClassOf(err) == ClassNotFound {
			return (&openAIModelsList{guard: d.guard}).listOpenAI(ctx, p)
		}
		return nil, err
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ModelName)
		if id == "" {
			continue
		}
		models = append(models, ModelInfo{
			ID:            id,
			DisplayName:   id,
			ContextWindow: positiveInt(m.ModelInfo.MaxInputTokens),
			MaxTokens:     positiveInt(m.ModelInfo.MaxOutputTokens),
		})
	}
	return models, nil
}

// positiveInt drops non-positive upstream limits so the column stays NULL.
func positiveInt(v *int) *int {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}
