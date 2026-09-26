package providerresolve

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type testTokenSource struct {
	token string
}

func (s *testTokenSource) Token() (string, error) {
	return s.token, nil
}

func (s *testTokenSource) RouteEligibility(context.Context) providers.RouteEligibility {
	return providers.RouteEligibility{Class: providers.RouteEligibilityHealthy}
}

type stubProvider struct {
	name  string
	model string
}

func (p *stubProvider) Chat(context.Context, providers.ChatRequest) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{FinishReason: "stop"}, nil
}

func (p *stubProvider) ChatStream(context.Context, providers.ChatRequest, func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{FinishReason: "stop"}, nil
}

func (p *stubProvider) DefaultModel() string { return p.model }
func (p *stubProvider) Name() string         { return p.name }

func TestResolveConfiguredProviderKeepsNonCodexBase(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	base := &stubProvider{name: "anthropic", model: "claude-sonnet-4"}
	registry.RegisterForTenant(tenantID, base)
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex-backup",
		&testTokenSource{token: "backup-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "anthropic",
		ChatGPTOAuthRouting: json.RawMessage(`{
			"strategy": "round_robin",
			"extra_provider_names": ["openai-codex-backup"]
		}`),
	}

	resolved, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		t.Fatalf("ResolveConfiguredProvider() error = %v", err)
	}
	if resolved != base {
		t.Fatalf("ResolveConfiguredProvider() returned %T, want original non-Codex provider", resolved)
	}
}

func TestResolveConfiguredProviderUsesRouterForCodexAgents(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex",
		&testTokenSource{token: "primary-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex-backup",
		&testTokenSource{token: "backup-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "openai-codex",
		ChatGPTOAuthRouting: json.RawMessage(`{
			"strategy": "round_robin",
			"extra_provider_names": ["openai-codex-backup"]
		}`),
	}

	resolved, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		t.Fatalf("ResolveConfiguredProvider() error = %v", err)
	}
	router, ok := resolved.(*providers.ChatGPTOAuthRouter)
	if !ok {
		t.Fatalf("ResolveConfiguredProvider() returned %T, want *providers.ChatGPTOAuthRouter", resolved)
	}
	if !router.HasAvailableProviders() {
		t.Fatal("router.HasAvailableProviders() = false, want true")
	}
	if router.Name() != "openai-codex" {
		t.Fatalf("router.Name() = %q, want %q", router.Name(), "openai-codex")
	}
}

func TestResolveConfiguredProviderUsesProviderDefaultsWhenAgentHasNoOverride(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex",
		&testTokenSource{token: "primary-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	).WithRoutingDefaults(store.ChatGPTOAuthStrategyRoundRobin, []string{"openai-codex-backup"}))
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex-backup",
		&testTokenSource{token: "backup-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "openai-codex",
	}

	resolved, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		t.Fatalf("ResolveConfiguredProvider() error = %v", err)
	}
	router, ok := resolved.(*providers.ChatGPTOAuthRouter)
	if !ok {
		t.Fatalf("ResolveConfiguredProvider() returned %T, want *providers.ChatGPTOAuthRouter", resolved)
	}
	if !router.HasAvailableProviders() {
		t.Fatal("router.HasAvailableProviders() = false, want true")
	}
}

func TestResolveConfiguredProviderKeepsExplicitSingleAccountOverride(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	baseProvider := providers.NewCodexProvider(
		"openai-codex",
		&testTokenSource{token: "primary-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	).WithRoutingDefaults(store.ChatGPTOAuthStrategyRoundRobin, []string{"openai-codex-backup"})
	registry.RegisterForTenant(tenantID, baseProvider)
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex-backup",
		&testTokenSource{token: "backup-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "openai-codex",
		ChatGPTOAuthRouting: json.RawMessage(`{
			"strategy": "manual"
		}`),
	}

	resolved, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		t.Fatalf("ResolveConfiguredProvider() error = %v", err)
	}
	router, ok := resolved.(*providers.ChatGPTOAuthRouter)
	if !ok {
		t.Fatalf("ResolveConfiguredProvider() returned %T, want *providers.ChatGPTOAuthRouter", resolved)
	}
	if router.Name() != "openai-codex" {
		t.Fatalf("router.Name() = %q, want %q", router.Name(), "openai-codex")
	}
}

type blockedTokenSource struct {
	token string
}

func (s *blockedTokenSource) Token() (string, error) {
	return s.token, nil
}

func (s *blockedTokenSource) RouteEligibility(context.Context) providers.RouteEligibility {
	return providers.RouteEligibility{Class: providers.RouteEligibilityBlocked, Reason: "reauth"}
}

func TestResolveConfiguredProviderReturnsRouterEvenWhenPrimaryNeedsFailover(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex",
		&blockedTokenSource{token: "primary-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))
	registry.RegisterForTenant(tenantID, providers.NewCodexProvider(
		"openai-codex-backup",
		&testTokenSource{token: "backup-token"},
		"http://127.0.0.1",
		"gpt-5.4",
	))

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "openai-codex",
		ChatGPTOAuthRouting: json.RawMessage(`{
			"strategy": "round_robin",
			"extra_provider_names": ["openai-codex-backup"]
		}`),
	}

	resolved, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		t.Fatalf("ResolveConfiguredProvider() error = %v", err)
	}
	if _, ok := resolved.(*providers.ChatGPTOAuthRouter); !ok {
		t.Fatalf("ResolveConfiguredProvider() returned %T, want *providers.ChatGPTOAuthRouter", resolved)
	}
}

func TestResolveAgentProviderWrapsModelFallback(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	base := &stubProvider{name: "primary", model: "primary-model"}
	backup := &stubProvider{name: "backup", model: "backup-default"}
	registry.RegisterForTenant(tenantID, base)
	registry.RegisterForTenant(tenantID, backup)

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "primary",
		Model:    "primary-model",
		ModelFallback: json.RawMessage(`{
			"enabled": true,
			"strategy": "priority_order",
			"candidates": [
				{"provider": "backup", "model": "backup-model"}
			]
		}`),
	}

	resolved, err := ResolveAgentProvider(context.Background(), registry, agent, ResolveInputs{})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() error = %v", err)
	}
	fallback, ok := resolved.(*providers.ModelFallbackProvider)
	if !ok {
		t.Fatalf("ResolveAgentProvider() returned %T, want *providers.ModelFallbackProvider", resolved)
	}
	if fallback.PrimaryProvider() != base {
		t.Fatalf("PrimaryProvider() = %T, want original base provider", fallback.PrimaryProvider())
	}
}

// recordingProvider records the order in which candidates were tried, so a test
// can assert the ORDER the fallback wrapper walks (the merged chain), not just
// that it eventually succeeded.
type recordingProvider struct {
	name  string
	model string
	err   error
	calls *[]string
}

func (p *recordingProvider) Chat(_ context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	*p.calls = append(*p.calls, p.name)
	if p.err != nil {
		return nil, p.err
	}
	return &providers.ChatResponse{Content: req.Model, FinishReason: "stop"}, nil
}

func (p *recordingProvider) ChatStream(_ context.Context, req providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return p.Chat(context.Background(), req)
}

func (p *recordingProvider) DefaultModel() string { return p.model }
func (p *recordingProvider) Name() string         { return p.name }

// TestResolveAgentProviderMergesProviderChainAfterAgentChain proves the documented
// precedence: the agent's own candidates run first, the provider-level
// settings.fallback_chain is appended, and a pair declared in both appears once.
func TestResolveAgentProviderMergesProviderChainAfterAgentChain(t *testing.T) {
	tenantID := uuid.New()
	var calls []string
	rateLimited := &providers.HTTPError{Status: 429, Body: "rate limited"}

	registry := providers.NewRegistry(nil)
	primary := &recordingProvider{name: "primary", model: "primary-model", err: rateLimited, calls: &calls}
	agentChoice := &recordingProvider{name: "agent-choice", model: "agent-model", err: rateLimited, calls: &calls}
	providerChoice := &recordingProvider{name: "provider-choice", model: "provider-model", calls: &calls}
	registry.RegisterForTenant(tenantID, primary)
	registry.RegisterForTenant(tenantID, agentChoice)
	registry.RegisterForTenant(tenantID, providerChoice)

	ps := newFakeProviderStore()
	// The provider-level chain repeats the agent's own pair, plus its own default.
	ps.addProvider("primary", `{"fallback_chain":[{"provider":"agent-choice","model":"agent-model"},{"provider":"provider-choice","model":"provider-model"}]}`)

	agent := &store.AgentData{
		TenantID: tenantID,
		Provider: "primary",
		Model:    "primary-model",
		ModelFallback: json.RawMessage(`{
			"enabled": true,
			"candidates": [{"provider": "agent-choice", "model": "agent-model"}]
		}`),
	}

	resolved, err := ResolveAgentProvider(context.Background(), registry, agent, ResolveInputs{Providers: ps})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() error = %v", err)
	}
	fallback, ok := resolved.(*providers.ModelFallbackProvider)
	if !ok {
		t.Fatalf("ResolveAgentProvider() returned %T, want *providers.ModelFallbackProvider", resolved)
	}
	if fallback.PrimaryProvider() != primary {
		t.Fatalf("PrimaryProvider() = %T, want the agent's own provider", fallback.PrimaryProvider())
	}

	if _, err := fallback.Chat(context.Background(), providers.ChatRequest{}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	want := []string{"primary", "agent-choice", "provider-choice"}
	if len(calls) != len(want) {
		t.Fatalf("candidate order = %v, want %v (agent chain first, provider chain appended, deduped)", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("candidate order = %v, want %v", calls, want)
		}
	}
}

// TestResolveAgentProviderUsesProviderChainAlone: a provider-level chain engages
// even when the agent declares no chain of its own.
func TestResolveAgentProviderUsesProviderChainAlone(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	base := &stubProvider{name: "primary", model: "primary-model"}
	backup := &stubProvider{name: "provider-choice", model: "provider-model"}
	registry.RegisterForTenant(tenantID, base)
	registry.RegisterForTenant(tenantID, backup)

	ps := newFakeProviderStore()
	ps.addProvider("primary", `{"fallback_chain":["provider-choice/provider-model"]}`)

	agent := &store.AgentData{TenantID: tenantID, Provider: "primary", Model: "primary-model"}

	resolved, err := ResolveAgentProvider(context.Background(), registry, agent, ResolveInputs{Providers: ps})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() error = %v", err)
	}
	if _, ok := resolved.(*providers.ModelFallbackProvider); !ok {
		t.Fatalf("ResolveAgentProvider() returned %T, want a fallback wrapper from the provider chain", resolved)
	}
}

// TestResolveAgentProviderWithoutChainsReturnsBase: no chain anywhere means no
// wrapper (the pre-phase-5 shape).
func TestResolveAgentProviderWithoutChainsReturnsBase(t *testing.T) {
	tenantID := uuid.New()
	registry := providers.NewRegistry(nil)
	base := &stubProvider{name: "primary", model: "primary-model"}
	registry.RegisterForTenant(tenantID, base)

	ps := newFakeProviderStore()
	ps.addProvider("primary", `{}`)

	agent := &store.AgentData{TenantID: tenantID, Provider: "primary", Model: "primary-model"}
	resolved, err := ResolveAgentProvider(context.Background(), registry, agent, ResolveInputs{Providers: ps})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() error = %v", err)
	}
	if resolved != base {
		t.Fatalf("ResolveAgentProvider() returned %T, want the bare base provider", resolved)
	}
}

// TestResolveAgentProviderPersistsCooldownThroughTheRealAdapter is the end-to-end
// wiring test: chain resolution builds the durable adapter, the primary answers
// 429, the secondary serves, and the failure reaches the store keyed by provider
// id — then a second resolution (fresh tracker = fresh process) still skips the
// primary.
func TestResolveAgentProviderPersistsCooldownThroughTheRealAdapter(t *testing.T) {
	tenantID := uuid.New()
	rateLimited := &providers.HTTPError{Status: 429, Body: "rate limited"}

	registry := providers.NewRegistry(nil)
	primary := &recordingProvider{name: "primary", model: "primary-model", err: rateLimited, calls: &[]string{}}
	secondary := &recordingProvider{name: "secondary", model: "secondary-model", calls: &[]string{}}
	registry.RegisterForTenant(tenantID, primary)
	registry.RegisterForTenant(tenantID, secondary)

	ps := newFakeProviderStore()
	primaryRow := ps.addProvider("primary", `{"fallback_chain":[{"provider":"secondary","model":"secondary-model"}]}`)
	ps.addProvider("secondary", `{}`)

	agent := &store.AgentData{TenantID: tenantID, Provider: "primary", Model: "primary-model"}
	resolved, err := ResolveAgentProvider(context.Background(), registry, agent, ResolveInputs{Providers: ps})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() error = %v", err)
	}
	fallback := resolved.(*providers.ModelFallbackProvider)

	// Two requests: the first records the failure, the second is the one probe the
	// cooldown allows, which persists the probe stamp (runOrdered asks availability
	// first, then probes).
	for range 2 {
		if _, err := fallback.Chat(context.Background(), providers.ChatRequest{}); err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
	}
	if ps.probeCount() == 0 {
		t.Fatal("expected the allowed probe to be persisted")
	}
	recorded, ok := ps.lastFailure()
	if !ok {
		t.Fatal("the 429 was not persisted through the cooldown adapter")
	}
	if recorded.providerID != primaryRow.ID {
		t.Errorf("persisted failure for provider id %s, want %s", recorded.providerID, primaryRow.ID)
	}
	if recorded.errorClass != string(providers.FailoverRateLimit) {
		t.Errorf("persisted error class = %q, want %q", recorded.errorClass, string(providers.FailoverRateLimit))
	}

	// A fresh resolution (new adapter + new tracker) over the same durable state
	// must skip the primary: the whole point of persisting the cooldown.
	probePrimary := &recordingProvider{name: "primary", model: "primary-model", calls: &[]string{}}
	probeSecondary := &recordingProvider{name: "secondary", model: "secondary-model", calls: &[]string{}}
	registry2 := providers.NewRegistry(nil)
	registry2.RegisterForTenant(tenantID, probePrimary)
	registry2.RegisterForTenant(tenantID, probeSecondary)

	resolved2, err := ResolveAgentProvider(context.Background(), registry2, agent, ResolveInputs{Providers: ps})
	if err != nil {
		t.Fatalf("ResolveAgentProvider() after restart error = %v", err)
	}
	fallback2 := resolved2.(*providers.ModelFallbackProvider)
	if _, err := fallback2.Chat(context.Background(), providers.ChatRequest{}); err != nil {
		t.Fatalf("Chat() after restart error = %v", err)
	}
	if len(*probePrimary.calls) != 0 {
		t.Errorf("primary was called after a restart despite the durable cooldown: %v", *probePrimary.calls)
	}
	if len(*probeSecondary.calls) != 1 {
		t.Errorf("secondary calls after restart = %v, want exactly 1", *probeSecondary.calls)
	}
}

// TestLookupProviderRowFallsBackToMasterScope: a tenant agent may run on a
// master-registered provider; the lookup must still find the row (and its
// fallback_chain) instead of silently dropping the provider-level chain.
func TestLookupProviderRowFallsBackToMasterScope(t *testing.T) {
	ps := &masterOnlyProviderStore{}
	row, err := lookupProviderRow(store.WithTenantID(context.Background(), uuid.New()), ps, "shared")
	if err != nil {
		t.Fatalf("lookupProviderRow() error = %v", err)
	}
	if row == nil || row.Name != "shared" {
		t.Fatalf("lookupProviderRow() = %+v, want the master-scoped row", row)
	}
}

// masterOnlyProviderStore only answers when the caller is in master scope, i.e.
// exactly the shape of a master-registered provider a tenant agent uses.
type masterOnlyProviderStore struct {
	fakeProviderStore
}

func (s *masterOnlyProviderStore) GetProviderByName(ctx context.Context, name string) (*store.LLMProviderData, error) {
	if store.TenantIDFromContext(ctx) != store.MasterTenantID {
		return nil, sql.ErrNoRows
	}
	row := &store.LLMProviderData{Name: name, Settings: []byte(`{"fallback_chain":["secondary/model"]}`)}
	return row, nil
}
