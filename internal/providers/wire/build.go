package wire

import (
	"errors"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

func init() {
	Register(Descriptor{
		API:                     OpenAICompletions,
		AuthKind:                AuthAPIKey,
		RequiresAPIKey:          true,
		DefaultBaseURL:          "https://api.openai.com/v1",
		ChatPath:                "/chat/completions",
		EnvKeys:                 []string{"OPENAI_API_KEY"},
		AuthHeaderStyle:         "bearer",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "o200k_base",
		Build:                   buildOpenAICompletions,
	})
	Register(Descriptor{
		API:                     OpenAIResponses,
		AuthKind:                AuthAPIKey,
		RequiresAPIKey:          true,
		DefaultBaseURL:          "https://api.openai.com/v1",
		ChatPath:                "/responses",
		EnvKeys:                 []string{"OPENAI_API_KEY"},
		AuthHeaderStyle:         "bearer",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "o200k_base",
		Build:                   buildOpenAIResponses,
	})
	Register(Descriptor{
		API:                     OpenAICodexResponses,
		AuthKind:                AuthOAuthBrowser,
		RequiresAPIKey:          true,
		DefaultBaseURL:          "https://chatgpt.com/backend-api",
		DefaultModel:            providers.DefaultCodexModel,
		ChatPath:                "/codex/responses",
		EnvKeys:                 []string{"CODEX_API_KEY", "OPENAI_API_KEY"},
		AuthHeaderStyle:         "oauth-bearer",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "o200k_base",
		Build:                   buildOpenAICodexResponses,
	})
	Register(Descriptor{
		API:                     AnthropicMessages,
		AuthKind:                AuthAPIKey,
		RequiresAPIKey:          true,
		ChatPath:                "/v1/messages",
		EnvKeys:                 []string{"ANTHROPIC_API_KEY"},
		AuthHeaderStyle:         "x-api-key",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "cl100k_base",
		Build:                   buildAnthropicMessages,
	})
	Register(Descriptor{
		API:                     GoogleGenerativeAI,
		AuthKind:                AuthAPIKey,
		RequiresAPIKey:          true,
		DefaultBaseURL:          "https://generativelanguage.googleapis.com/v1beta/openai",
		DefaultModel:            "gemini-2.0-flash",
		ChatPath:                "/chat/completions",
		EnvKeys:                 []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		AuthHeaderStyle:         "bearer",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "o200k_base",
		Build:                   buildOpenAICompletions,
	})
	Register(Descriptor{
		API:                     GoogleVertex,
		AuthKind:                AuthServiceAccount,
		RequiresAPIKey:          false,
		ChatPath:                "/chat/completions",
		EnvKeys:                 []string{"GOOGLE_APPLICATION_CREDENTIALS"},
		AuthHeaderStyle:         "gcp-oauth2",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "o200k_base",
		Build:                   buildGoogleVertex,
	})
	Register(Descriptor{
		API:                     OllamaNative,
		AuthKind:                AuthNone,
		RequiresAPIKey:          false,
		DefaultBaseURL:          "http://localhost:11434",
		DefaultModel:            DefaultOllamaModel,
		ChatPath:                "/api/chat",
		EnvKeys:                 []string{"OLLAMA_API_KEY"},
		AuthHeaderStyle:         "none",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		Build:                   buildOllamaNative,
	})
	Register(Descriptor{
		API:                     CLIDelegated,
		AuthKind:                AuthCLIDelegated,
		RequiresAPIKey:          false,
		ChatPath:                "",
		AuthHeaderStyle:         "none",
		SupportsTools:           true,
		SupportsStream:          true,
		SupportsStreamWithTools: true,
		TokenizerID:             "cl100k_base",
		Build:                   buildCLIDelegated,
	})
}

// buildOpenAICompletions builds every Chat Completions transport: the plain
// OpenAI-compatible provider, plus the two brands whose wire shape needs a
// wrapper (dashscope degrades tools+stream, aimlapi adds partner headers).
func buildOpenAICompletions(cfg Config) (providers.Provider, error) {
	brand := BrandOrDefault(cfg.ProviderType)
	base := firstNonEmpty(cfg.BaseURL, brand.BaseURL)
	model := firstNonEmpty(cfg.DefaultModel, brand.Model)

	var wrapper providers.Provider
	var prov *providers.OpenAIProvider
	switch brand.Constructor {
	case constructorDashScope:
		ds := providers.NewDashScopeProvider(cfg.Name, cfg.APIKey, base, model)
		wrapper, prov = ds, ds.OpenAIProvider
	case constructorAIMLAPI:
		// The aimlapi constructor owns its base URL, default model and partner
		// headers; the brand catalog mirrors them for BrandDefaults callers.
		prov = providers.NewAIMLAPIProvider(cfg.Name, cfg.APIKey, base)
	default:
		prov = providers.NewOpenAIProvider(cfg.Name, cfg.APIKey, base, model)
	}

	applyOpenAIOptions(prov, brand, cfg)
	if wrapper != nil {
		return wrapper, nil
	}
	return prov, nil
}

// buildOpenAIResponses is the non-Codex Responses API. GoClaw has no transport
// for it, so a row that declares it is rejected with an actionable error rather
// than silently downgraded to Chat Completions.
func buildOpenAIResponses(cfg Config) (providers.Provider, error) {
	return nil, errors.New("no openai-responses transport in this build; declare wire_api 'openai-codex-responses' for the ChatGPT/Codex flow")
}

// buildOpenAICodexResponses builds the ChatGPT subscription (Codex) transport.
// It needs a TokenSource (the OAuth credential layer), never a static key.
func buildOpenAICodexResponses(cfg Config) (providers.Provider, error) {
	if cfg.TokenSource == nil {
		return nil, errors.New("no token source supplied; openai-codex-responses requires an OAuth credential")
	}
	brand := BrandOrDefault(cfg.ProviderType)
	prov := providers.NewCodexProvider(cfg.Name, cfg.TokenSource,
		firstNonEmpty(cfg.BaseURL, brand.BaseURL),
		firstNonEmpty(cfg.DefaultModel, brand.Model))
	if cfg.Routing != nil {
		prov.WithRoutingDefaults(cfg.Routing.Strategy, cfg.Routing.ExtraProviderNames)
	}
	if cfg.Timeout > 0 {
		prov.WithRequestTimeout(cfg.Timeout)
	}
	if cfg.ConcurrencyGate != nil {
		prov.WithConcurrencyGate(cfg.ConcurrencyGate)
	}
	return prov, nil
}

// buildAnthropicMessages builds the Anthropic Messages API transport.
func buildAnthropicMessages(cfg Config) (providers.Provider, error) {
	brand := BrandOrDefault(cfg.ProviderType)
	opts := []providers.AnthropicOption{
		providers.WithAnthropicName(cfg.Name),
		providers.WithAnthropicBaseURL(firstNonEmpty(cfg.BaseURL, brand.BaseURL)),
	}
	if model := firstNonEmpty(cfg.DefaultModel, brand.Model); model != "" {
		opts = append(opts, providers.WithAnthropicModel(model))
	}
	if cfg.Registry != nil {
		opts = append(opts, providers.WithAnthropicRegistry(cfg.Registry))
	}
	prov := providers.NewAnthropicProvider(cfg.APIKey, opts...)
	if cfg.Timeout > 0 {
		prov.WithRequestTimeout(cfg.Timeout)
	}
	if cfg.ConcurrencyGate != nil {
		prov.WithConcurrencyGate(cfg.ConcurrencyGate)
	}
	return prov, nil
}

// buildGoogleVertex builds the Vertex AI transport from a service-account
// declaration (inline JSON, credentials file, or ADC).
func buildGoogleVertex(cfg Config) (providers.Provider, error) {
	if cfg.Vertex == nil {
		return nil, errors.New("settings must declare project_id and region")
	}
	prov, err := providers.NewVertexProviderWithTimeout(providers.VertexConfig{
		Name:            cfg.Name,
		CredentialsJSON: cfg.APIKey,
		CredentialsFile: cfg.Vertex.CredentialsFile,
		ProjectID:       cfg.Vertex.ProjectID,
		Region:          cfg.Vertex.Region,
		DefaultModel:    firstNonEmpty(cfg.DefaultModel, cfg.Vertex.Model),
		APIBaseOverride: cfg.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	if cfg.Timeout > 0 {
		prov.WithRequestTimeout(cfg.Timeout)
	}
	if cfg.ConcurrencyGate != nil {
		prov.WithConcurrencyGate(cfg.ConcurrencyGate)
	}
	return prov, nil
}

// buildOllamaNative builds the native Ollama /api/chat transport for local and
// cloud endpoints. The base URL default is applied before the Docker host
// rewrite, so a container reaches the host, not itself.
func buildOllamaNative(cfg Config) (providers.Provider, error) {
	brand := BrandOrDefault(cfg.ProviderType)
	base := firstNonEmpty(cfg.BaseURL, brand.BaseURL, "http://localhost:11434")
	model := firstNonEmpty(cfg.DefaultModel, brand.Model, DefaultOllamaModel)
	prov := providers.NewOllamaProvider(cfg.Name, config.DockerLocalhost(base), model, cfg.OllamaNumCtx, nil)
	if cfg.Thinking != nil && brand.ThinkingFromSettings {
		prov.WithThinkingEnabled(cfg.Thinking)
	}
	if cfg.Timeout > 0 {
		prov.WithRequestTimeout(cfg.Timeout)
	}
	if cfg.ConcurrencyGate != nil {
		prov.WithConcurrencyGate(cfg.ConcurrencyGate)
	}
	return prov, nil
}

// buildCLIDelegated builds a subprocess transport. Both claude_cli and acp speak
// cli-delegated; the brand table selects the contract, so the registry — not the
// registration call site — owns that decision.
func buildCLIDelegated(cfg Config) (providers.Provider, error) {
	cli := cfg.CLI
	if cli == nil {
		return nil, errors.New("no cli configuration supplied")
	}
	if cli.Path == "" {
		return nil, errors.New("no executable path supplied")
	}
	switch BrandOrDefault(cfg.ProviderType).CLIKind {
	case CLIKindClaude:
		var opts []providers.ClaudeCLIOption
		opts = append(opts, providers.WithClaudeCLIName(cli.Name))
		if cli.Model != "" {
			opts = append(opts, providers.WithClaudeCLIModel(cli.Model))
		}
		if cli.WorkDir != "" {
			opts = append(opts, providers.WithClaudeCLIWorkDir(cli.WorkDir))
		}
		if cli.PermMode != "" {
			opts = append(opts, providers.WithClaudeCLIPermMode(cli.PermMode))
		}
		if cli.MCP != nil {
			opts = append(opts, providers.WithClaudeCLIMCPConfigData(cli.MCP))
		}
		if cli.SecurityHooks {
			opts = append(opts, providers.WithClaudeCLISecurityHooks(cli.WorkDir, true, cli.DenyPatterns))
		}
		return providers.NewClaudeCLIProvider(cli.Path, opts...), nil
	case CLIKindACP:
		var opts []providers.ACPOption
		if cli.Name != "" {
			opts = append(opts, providers.WithACPName(cli.Name))
		}
		if cli.Model != "" {
			opts = append(opts, providers.WithACPModel(cli.Model))
		}
		if cli.PermMode != "" {
			opts = append(opts, providers.WithACPPermMode(cli.PermMode))
		}
		return providers.NewACPProvider(cli.Path, cli.Args, cli.WorkDir, cli.IdleTTL, cli.DenyPatterns, opts...), nil
	default:
		return nil, fmt.Errorf("brand %q is not a cli-delegated transport (expected %s or %s)",
			cfg.ProviderType, CLIKindClaude, CLIKindACP)
	}
}

// applyOpenAIOptions applies the brand data and per-row overrides that every
// OpenAI-compatible transport shares. Order matters only for timeout, which is
// applied last so it survives Vertex's own client.
func applyOpenAIOptions(prov *providers.OpenAIProvider, brand Brand, cfg Config) {
	if reflectType(brand, cfg.Source) && cfg.ProviderType != "" {
		prov.WithProviderType(cfg.ProviderType)
	}
	if headers := cloneHeaders(brand.ExtraHeaders); len(headers) > 0 {
		prov.WithExtraHeaders(headers)
	}
	if brand.SiteURL != "" || brand.SiteTitle != "" {
		prov.WithSiteInfo(brand.SiteURL, brand.SiteTitle)
	}
	if cfg.Registry != nil {
		prov.WithRegistry(cfg.Registry)
	}
	if cfg.Thinking != nil && brand.ThinkingFromSettings {
		prov.WithThinkingEnabled(cfg.Thinking)
	}
	if cfg.Timeout > 0 {
		prov.WithRequestTimeout(cfg.Timeout)
	}
	if cfg.ConcurrencyGate != nil {
		prov.WithConcurrencyGate(cfg.ConcurrencyGate)
	}
	// Compatibility: the declaration (provider_type, endpoint family, operator
	// quirks) then one resolution per catalogue model. Everything below
	// refreshes at most once per provider build; the request path only reads.
	if cfg.EndpointFamily != "" {
		prov.WithEndpointFamily(cfg.EndpointFamily)
	}
	if len(cfg.Quirks) > 0 {
		prov.WithQuirks(cfg.Quirks)
	}
	if cfg.OllamaNumCtx != nil {
		prov.WithOllamaNumCtx(*cfg.OllamaNumCtx)
	}
	if len(cfg.ModelCompat) > 0 {
		prov.WithModelCompat(cfg.ModelCompat)
	}
}

// reflectType reports whether the brand's provider_type is passed into the
// transport at this registration call site.
func reflectType(brand Brand, source Source) bool {
	switch brand.Reflect {
	case ReflectConfigAndDB:
		return true
	case ReflectDBOnly:
		return source == SourceDB
	default:
		return false
	}
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
