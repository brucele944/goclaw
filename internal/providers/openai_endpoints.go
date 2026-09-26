package providers

import (
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
)

// isOpenAINativeEndpoint reports whether apiBase is first-party OpenAI
// infrastructure that accepts the "developer" message role and the
// prompt-cache parameters. The base-URL knowledge lives in the compat family
// rules so there is exactly one definition; matching OpenClaw TS
// model-compat.ts → isOpenAINativeEndpoint().
func isOpenAINativeEndpoint(apiBase string) bool {
	return compat.IsNativeOpenAIEndpoint(apiBase)
}

// ollamaNativeURL returns the full URL for Ollama's native endpoint carried by
// the resolved compat object (Ollama's OpenAI-compat shim at
// /v1/chat/completions silently ignores options.num_ctx, while /api/chat
// honours it). The apiBase may include a /v1 suffix
// (e.g. "http://localhost:11434/v1") — it is stripped before appending the path.
func (p *OpenAIProvider) ollamaNativeURL() string {
	path := "/api/chat"
	if p.compat != nil && p.compat.NativeChatPath != "" {
		path = p.compat.NativeChatPath
	}
	base := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(p.apiBase, "/"), "/v1"), "/")
	return base + path
}
