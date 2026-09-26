package discovery

import "context"

// proxyDiscovery lists models from a multi-vendor proxy (OpenRouter and friends).
//
// A proxy serves several vendors behind one endpoint, so the vendor of a model
// is only known per model: it is read from the "<vendor>/<model>" id the proxy
// itself uses (openrouter, litellm, …). When the OpenAI-shaped /models path is
// absent (404/405) the Anthropic-shaped listing is tried, which is what
// "auto-detect per model" means in practice for a proxy that fronts both APIs.
type proxyDiscovery struct {
	guard URLGuard
}

func (d *proxyDiscovery) Type() string { return TypeProxy }

func (d *proxyDiscovery) List(ctx context.Context, p ProviderRef) ([]ModelInfo, error) {
	models, err := (&openAIModelsList{guard: d.guard}).listOpenAI(ctx, p)
	if err == nil {
		return models, nil
	}
	if ClassOf(err) != ClassNotFound {
		return nil, err
	}
	// The proxy does not speak the OpenAI listing shape; try the Anthropic one
	// (auto-detect per model, per the vendor's own id namespace).
	return (&openAIModelsList{guard: d.guard}).listAnthropic(ctx, p)
}
