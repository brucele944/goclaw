package providers

import (
	"context"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/providers/compat"
)

// catalogueProvider is a minimal transport used to probe the capability
// resolution helpers. It carries no catalogue data of its own — the overrides
// come from the injected lookup, exactly like the request path.
type catalogueProvider struct {
	name string
	caps ProviderCapabilities
}

func (p *catalogueProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{Content: "ok"}, nil
}

func (p *catalogueProvider) ChatStream(context.Context, ChatRequest, func(StreamChunk)) (*ChatResponse, error) {
	return &ChatResponse{Content: "ok"}, nil
}

func (p *catalogueProvider) DefaultModel() string               { return "test-model" }
func (p *catalogueProvider) Name() string                       { return p.name }
func (p *catalogueProvider) Capabilities() ProviderCapabilities { return p.caps }

// compatCatalogueProvider additionally exposes a phase-4 resolved compat object.
type compatCatalogueProvider struct {
	catalogueProvider
	resolved *compat.Resolved
}

func (p *compatCatalogueProvider) Compat() *compat.Resolved { return p.resolved }

// TestResolveModelCapabilities_OverridesCataloguedRowOnly verifies the merge
// rule the request path relies on: a row that declares a key replaces that key,
// a row that stays silent leaves the provider's own declaration alone, and the
// row's context window travels separately as a clamp.
func TestResolveModelCapabilities_OverridesCataloguedRowOnly(t *testing.T) {
	base := ProviderCapabilities{
		Streaming:        true,
		ToolCalling:      true,
		StreamWithTools:  true,
		Vision:           true,
		MaxContextWindow: 128_000,
	}
	lookup := func(providerName, providerType, model string) (ModelCapabilityOverride, bool) {
		if providerType != "acme-mini" || model != "fast-1" {
			return ModelCapabilityOverride{}, false
		}
		return ModelCapabilityOverride{
			ToolCalling:      new(false),
			StreamWithTools:  new(false),
			MaxContextWindow: 65_536,
		}, true
	}

	res := ResolveModelCapabilities(base, lookup, "acme", "acme-mini", "fast-1")
	if !res.ProviderDeclared {
		t.Fatal("ProviderDeclared must be true when the provider declares capabilities")
	}
	if res.Capabilities.ToolCalling {
		t.Error("row tool_calling=false must override the provider's ToolCalling=true")
	}
	if res.Capabilities.StreamWithTools {
		t.Error("row stream_with_tools=false must override the provider's StreamWithTools=true")
	}
	if !res.Capabilities.Vision {
		t.Error("a key the row does not mention (vision) must be inherited from the provider")
	}
	if !res.Capabilities.Streaming {
		t.Error("Streaming is not a per-model key and must be inherited unchanged")
	}
	if res.Capabilities.MaxContextWindow != 128_000 {
		t.Errorf("MaxContextWindow = %d, want the provider default 128000 (the row's window is a clamp, not a default)",
			res.Capabilities.MaxContextWindow)
	}
	if res.ContextWindowClamp != 65_536 {
		t.Errorf("ContextWindowClamp = %d, want the row's declared 65536", res.ContextWindowClamp)
	}

	// Until the model is catalogued the provider's declaration stands unchanged.
	uncatalogued := ResolveModelCapabilities(base, lookup, "acme", "acme-mini", "slow-1")
	if !uncatalogued.Capabilities.ToolCalling || uncatalogued.ContextWindowClamp != 0 {
		t.Errorf("uncatalogued model must keep the provider capabilities and declare no clamp: %+v", uncatalogued)
	}
}

// TestResolveModelCapabilities_NilLookupKeepsProviderDeclaration covers the
// Lite/isolated wiring: without a lookup the provider's own capabilities are the
// effective ones and no clamp is applied.
func TestResolveModelCapabilities_NilLookupKeepsProviderDeclaration(t *testing.T) {
	base := ProviderCapabilities{Streaming: true, ToolCalling: true, Vision: true}
	res := ResolveModelCapabilities(base, nil, "acme", "acme-mini", "fast-1")
	if res.Capabilities != base {
		t.Errorf("capabilities = %+v, want the base declaration %+v", res.Capabilities, base)
	}
	if res.ContextWindowClamp != 0 {
		t.Errorf("ContextWindowClamp = %d, want 0 without a lookup", res.ContextWindowClamp)
	}
}

// TestCacheBreakpointsSupported_CapabilityOrCompatDeclaresIt verifies the two
// independent declarations that enable prompt-cache parameters. The provider's
// Go type is deliberately not consulted — the old behaviour was a type switch on
// Codex/ChatGPTOAuth, which never matched a catalogue row.
func TestCacheBreakpointsSupported_CapabilityOrCompatDeclaresIt(t *testing.T) {
	plain := &catalogueProvider{name: "acme-mini", caps: ProviderCapabilities{Streaming: true, ToolCalling: true}}
	if CacheBreakpointsSupported(plain, plain.caps) {
		t.Error("a provider that declares neither cache capability nor compat cache support must not get cache parameters")
	}

	rowDeclared := &catalogueProvider{name: "acme-mini", caps: ProviderCapabilities{CacheControl: true}}
	if !CacheBreakpointsSupported(rowDeclared, rowDeclared.caps) {
		t.Error("CacheControl=true on the effective capabilities must enable cache parameters")
	}

	compatDeclared := &compatCatalogueProvider{
		catalogueProvider: catalogueProvider{name: "acme-mini", caps: ProviderCapabilities{}},
		resolved:          &compat.Resolved{SystemCacheControl: true},
	}
	if !CacheBreakpointsSupported(compatDeclared, compatDeclared.caps) {
		t.Error("a resolved compat object declaring SystemCacheControl must enable cache parameters")
	}

	toolPrefixOnly := &compatCatalogueProvider{
		catalogueProvider: catalogueProvider{name: "acme-mini", caps: ProviderCapabilities{}},
		resolved:          &compat.Resolved{ToolPrefixCache: true},
	}
	if !CacheBreakpointsSupported(toolPrefixOnly, toolPrefixOnly.caps) {
		t.Error("a resolved compat object declaring ToolPrefixCache must enable cache parameters")
	}

	compatSilent := &compatCatalogueProvider{
		catalogueProvider: catalogueProvider{name: "acme-mini", caps: ProviderCapabilities{}},
		resolved:          &compat.Resolved{},
	}
	if CacheBreakpointsSupported(compatSilent, compatSilent.caps) {
		t.Error("a compat object that declares nothing must not enable cache parameters")
	}
}
