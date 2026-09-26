package providers

// Compile-time guards for the provider interfaces that the runtime depends on.
// They exist so a future refactor cannot silently drop a method from a provider
// or widen the optional capability surface: the build fails instead.

var (
	_ Provider = (*AnthropicProvider)(nil)
	_ Provider = (*OpenAIProvider)(nil)
	_ Provider = (*DashScopeProvider)(nil)
	_ Provider = (*CodexProvider)(nil)
	_ Provider = (*ACPProvider)(nil)
	_ Provider = (*OllamaProvider)(nil)
	_ Provider = (*ClaudeCLIProvider)(nil)
	_ Provider = (*ModelFallbackProvider)(nil)

	_ CapabilitiesAware = (*AnthropicProvider)(nil)
	_ CapabilitiesAware = (*OpenAIProvider)(nil)
	_ CapabilitiesAware = (*DashScopeProvider)(nil)
	_ CapabilitiesAware = (*CodexProvider)(nil)
	_ CapabilitiesAware = (*ACPProvider)(nil)
	_ CapabilitiesAware = (*OllamaProvider)(nil)
	_ CapabilitiesAware = (*ClaudeCLIProvider)(nil)
)
