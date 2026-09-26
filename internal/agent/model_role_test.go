package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// roleCaptureProvider records every chat request the run actually dispatched.
type roleCaptureProvider struct {
	name     string
	requests []providers.ChatRequest
}

func (p *roleCaptureProvider) Chat(_ context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return &providers.ChatResponse{
		Content: "ok",
		Usage:   &providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func (p *roleCaptureProvider) ChatStream(ctx context.Context, req providers.ChatRequest, _ func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func (p *roleCaptureProvider) DefaultModel() string { return p.name + "-default" }
func (p *roleCaptureProvider) Name() string         { return p.name }

// newRoleTestLoop builds a runnable Loop whose primary is `primary` / `primaryModel`
// and which declares the given resolved role targets.
func newRoleTestLoop(t *testing.T, primary providers.Provider, primaryModel string, roles map[string]ModelRoleTarget) *Loop {
	t.Helper()
	ws := t.TempDir()
	return NewLoop(LoopConfig{
		ID:            "role-agent",
		Provider:      primary,
		Model:         primaryModel,
		ModelRoles:    roles,
		ContextWindow: 128_000,
		MaxIterations: 1,
		Workspace:     ws,
		DataDir:       ws,
		Tools:         tools.NewRegistry(),
		Sessions:      &nopSessionStore{},
	})
}

// TestModelRoleWinsOverAgentPrimary — an agent with both a role mapping and a
// primary provider/model routes the run that requests the role to the role's
// provider and model, and never touches the primary provider.
func TestModelRoleWinsOverAgentPrimary(t *testing.T) {
	primary := &roleCaptureProvider{name: "primary-provider"}
	roleProvider := &roleCaptureProvider{name: "role-provider"}
	loop := newRoleTestLoop(t, primary, "primary-model", map[string]ModelRoleTarget{
		"coder": {ProviderName: "role-provider", Model: "role-model", Provider: roleProvider},
	})

	ctx := WithModelRole(context.Background(), "coder")
	if _, err := loop.Run(ctx, RunRequest{SessionKey: "agent:role-agent:test", RunID: "run-role", Message: "hi"}); err != nil {
		t.Fatalf("run with role: %v", err)
	}

	if len(roleProvider.requests) != 1 {
		t.Fatalf("role provider requests = %d, want 1", len(roleProvider.requests))
	}
	if got := roleProvider.requests[0].Model; got != "role-model" {
		t.Fatalf("role provider received model %q, want %q", got, "role-model")
	}
	if len(primary.requests) != 0 {
		t.Fatalf("primary provider received %d requests, want 0 (role must win)", len(primary.requests))
	}
}

// TestExplicitOverrideBeatsModelRole — an explicit per-request override wins over
// a requested role, and a role that was not declared leaves the agent primary in
// charge.
func TestExplicitOverrideBeatsModelRole(t *testing.T) {
	primary := &roleCaptureProvider{name: "primary-provider"}
	roleProvider := &roleCaptureProvider{name: "role-provider"}
	override := &roleCaptureProvider{name: "override-provider"}
	loop := newRoleTestLoop(t, primary, "primary-model", map[string]ModelRoleTarget{
		"coder": {ProviderName: "role-provider", Model: "role-model", Provider: roleProvider},
	})

	ctx := WithModelRole(context.Background(), "coder")
	_, err := loop.Run(ctx, RunRequest{
		SessionKey:      "agent:role-agent:test",
		RunID:           "run-override",
		Message:         "hi",
		ModelOverride:   "explicit-model",
		ProviderOverride: override,
	})
	if err != nil {
		t.Fatalf("run with explicit override: %v", err)
	}

	if len(override.requests) != 1 || override.requests[0].Model != "explicit-model" {
		t.Fatalf("override provider requests = %+v, want one request with model %q", override.requests, "explicit-model")
	}
	if len(roleProvider.requests) != 0 {
		t.Fatalf("role provider received %d requests, want 0 (explicit override wins)", len(roleProvider.requests))
	}
	if len(primary.requests) != 0 {
		t.Fatalf("primary provider received %d requests, want 0", len(primary.requests))
	}
}

// TestUnknownModelRoleFallsBackToAgentPrimary — a role the agent does not declare
// (or whose provider never registered) degrades to the agent primary instead of
// routing somewhere unexpected.
func TestUnknownModelRoleFallsBackToAgentPrimary(t *testing.T) {
	primary := &roleCaptureProvider{name: "primary-provider"}
	// Provider == nil mirrors a declared role whose provider is not in the registry.
	loop := newRoleTestLoop(t, primary, "primary-model", map[string]ModelRoleTarget{
		"coder": {ProviderName: "ghost-provider", Model: "ghost-model"},
	})

	for _, role := range []string{"summarizer", "coder"} {
		ctx := WithModelRole(context.Background(), role)
		if _, err := loop.Run(ctx, RunRequest{SessionKey: "agent:role-agent:test", RunID: "run-" + role, Message: "hi"}); err != nil {
			t.Fatalf("run with role %q: %v", role, err)
		}
	}

	if len(primary.requests) != 2 {
		t.Fatalf("primary provider requests = %d, want 2 (both roles degrade to agent primary)", len(primary.requests))
	}
	for i, req := range primary.requests {
		if req.Model != "primary-model" {
			t.Fatalf("request %d model = %q, want %q", i, req.Model, "primary-model")
		}
	}
}

// TestModelOverrideBypassesFallbackChain — a per-request model override runs on
// the fallback wrapper's primary provider and never attempts a fallback
// candidate, which is what "explicit overrides bypass fallback" means downstream.
func TestModelOverrideBypassesFallbackChain(t *testing.T) {
	primary := &roleCaptureProvider{name: "fallback-primary"}
	secondary := &roleCaptureProvider{name: "fallback-secondary"}
	wrapper := providers.NewModelFallbackProvider(
		providers.FallbackCandidate{ProviderName: "fallback-primary", Model: "primary-model", Provider: primary},
		[]providers.FallbackCandidate{{ProviderName: "fallback-secondary", Model: "secondary-model", Provider: secondary}},
		0,
		false,
		nil,
	)
	loop := newRoleTestLoop(t, wrapper, "primary-model", nil)

	_, err := loop.Run(context.Background(), RunRequest{
		SessionKey:    "agent:role-agent:test",
		RunID:         "run-fallback-bypass",
		Message:       "hi",
		ModelOverride: "override-model",
	})
	if err != nil {
		t.Fatalf("run with fallback wrapper: %v", err)
	}

	if len(primary.requests) != 1 || primary.requests[0].Model != "override-model" {
		t.Fatalf("primary requests = %+v, want one request with model %q", primary.requests, "override-model")
	}
	if len(secondary.requests) != 0 {
		t.Fatalf("fallback candidate was attempted %d times, want 0 (override must bypass fallback)", len(secondary.requests))
	}
}

// TestResolveModelRoleTargetsUsesRegistry — the resolver turns agents.model_roles
// into targets with live providers, tenant-aware, and keeps unresolvable roles
// (provider nil) so the run degrades to the agent primary rather than failing.
func TestResolveModelRoleTargetsUsesRegistry(t *testing.T) {
	reg := providers.NewRegistry(func(context.Context) uuid.UUID { return store.MasterTenantID })
	reg.RegisterForTenant(store.MasterTenantID, &roleCaptureProvider{name: "anthropic"})
	deps := ResolverDeps{ProviderReg: reg}

	ag := &store.AgentData{
		AgentKey: "role-agent",
		TenantID: store.MasterTenantID,
		ModelRoles: json.RawMessage(`{"coder":"anthropic/claude-sonnet-4-5","summarizer":"ghost/haiku"}`),
	}

	targets := resolveModelRoleTargets(deps, ag)
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want 2 entries", targets)
	}
	coder := targets["coder"]
	if coder.Provider == nil || coder.Provider.Name() != "anthropic" || coder.Model != "claude-sonnet-4-5" {
		t.Fatalf("coder target = %+v, want anthropic/claude-sonnet-4-5 with a live provider", coder)
	}
	if summarizer := targets["summarizer"]; summarizer.Provider != nil || summarizer.Model != "haiku" {
		t.Fatalf("summarizer target = %+v, want model haiku with nil provider (not registered)", summarizer)
	}

	// A role whose provider is missing must not be applied to the run.
	req := RunRequest{}
	applyModelRoleOverride(WithModelRole(context.Background(), "summarizer"), &req, targets)
	if req.ModelOverride != "" || req.ProviderOverride != nil {
		t.Fatalf("unresolvable role applied an override: model=%q provider=%v", req.ModelOverride, req.ProviderOverride)
	}
}
