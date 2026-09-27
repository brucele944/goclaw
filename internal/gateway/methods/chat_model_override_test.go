package methods

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/gateway"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// ─── fakes for the chat.send per-request override path ───────────────────────

// overrideProvider is a registry entry with no I/O.
type overrideProvider struct{ name string }

func (p *overrideProvider) Chat(context.Context, providers.ChatRequest) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{Content: "ok"}, nil
}

func (p *overrideProvider) ChatStream(context.Context, providers.ChatRequest, func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return &providers.ChatResponse{Content: "ok"}, nil
}

func (p *overrideProvider) DefaultModel() string { return p.name + "-default" }
func (p *overrideProvider) Name() string         { return p.name }

// overrideAgent records the RunRequest chat.send hands to the loop layer.
type overrideAgent struct {
	provider providers.Provider
	requests chan agent.RunRequest
}

func (a *overrideAgent) ID() string                   { return "override-agent" }
func (a *overrideAgent) UUID() uuid.UUID              { return uuid.Nil }
func (a *overrideAgent) OtherConfig() json.RawMessage { return nil }
func (a *overrideAgent) IsRunning() bool              { return false }
func (a *overrideAgent) Model() string                { return "primary-model" }
func (a *overrideAgent) ProviderName() string         { return a.provider.Name() }
func (a *overrideAgent) Provider() providers.Provider { return a.provider }

func (a *overrideAgent) Run(_ context.Context, req agent.RunRequest) (*agent.RunResult, error) {
	a.requests <- req
	return &agent.RunResult{Content: "ok", RunID: req.RunID}, nil
}

// overrideSessionStore implements only what the dispatch path touches. The
// non-empty label short-circuits post-run conversation-title generation.
type overrideSessionStore struct {
	store.SessionStore
}

func (overrideSessionStore) GetLabel(context.Context, string) string { return "existing" }

// newOverrideChatMethods wires ChatMethods against a resolver-backed router and a
// provider registry holding one extra provider named "alt-provider".
func newOverrideChatMethods(t *testing.T) (*ChatMethods, *overrideAgent) {
	t.Helper()
	ag := &overrideAgent{
		provider: &overrideProvider{name: "agent-provider"},
		requests: make(chan agent.RunRequest, 4),
	}
	router := agent.NewRouter()
	router.SetResolver(func(context.Context, string) (agent.Agent, error) { return ag, nil })

	reg := providers.NewRegistry(func(context.Context) uuid.UUID { return store.MasterTenantID })
	reg.RegisterForTenant(store.MasterTenantID, &overrideProvider{name: "alt-provider"})

	cfg := &config.Config{}
	cfg.Gateway.InboundDebounceMs = 0 // dispatch inline, no debounce timer
	m := NewChatMethods(router, overrideSessionStore{}, cfg, nil, nil)
	m.SetProviderRegistry(reg)
	return m, ag
}

func sendChatParams(t *testing.T, params map[string]any) *protocol.RequestFrame {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return &protocol.RequestFrame{Type: "req", ID: "req-1", Method: protocol.MethodChatSend, Params: raw}
}

func awaitChatRunRequest(t *testing.T, ag *overrideAgent) agent.RunRequest {
	t.Helper()
	select {
	case req := <-ag.requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("chat.send never reached the agent loop")
		return agent.RunRequest{}
	}
}

// TestChatSendModelOverrideReachesLoop — chat.send `model` + `provider` must reach
// the run as RunRequest.ModelOverride/ProviderOverride, with the provider name
// resolved through the live registry (not passed through as a string).
func TestChatSendModelOverrideReachesLoop(t *testing.T) {
	m, ag := newOverrideChatMethods(t)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	client := gateway.NewTestClient(permissions.RoleAdmin, store.MasterTenantID, "user-1")

	m.handleSend(ctx, client, sendChatParams(t, map[string]any{
		"message":  "hello",
		"agentId":  "override-agent",
		"model":    "override-model",
		"provider": "alt-provider",
	}))

	req := awaitChatRunRequest(t, ag)
	if req.ModelOverride != "override-model" {
		t.Fatalf("ModelOverride = %q, want %q (run would use the agent primary model)", req.ModelOverride, "override-model")
	}
	if req.ProviderOverride == nil {
		t.Fatal("ProviderOverride = nil, want the registered alt-provider")
	}
	if got := req.ProviderOverride.Name(); got != "alt-provider" {
		t.Fatalf("ProviderOverride.Name() = %q, want %q", got, "alt-provider")
	}
}

// TestChatSendModelOnlyOverrideReachesLoop — `model` without `provider` still
// overrides the model and leaves provider selection to the agent (the fallback
// wrapper's primary; see Loop.runViaPipeline).
func TestChatSendModelOnlyOverrideReachesLoop(t *testing.T) {
	m, ag := newOverrideChatMethods(t)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	client := gateway.NewTestClient(permissions.RoleAdmin, store.MasterTenantID, "user-1")

	m.handleSend(ctx, client, sendChatParams(t, map[string]any{
		"message": "hello",
		"agentId": "override-agent",
		"model":   "solo-model",
	}))

	req := awaitChatRunRequest(t, ag)
	if req.ModelOverride != "solo-model" {
		t.Fatalf("ModelOverride = %q, want %q", req.ModelOverride, "solo-model")
	}
	if req.ProviderOverride != nil {
		t.Fatalf("ProviderOverride = %v, want nil when no provider was requested", req.ProviderOverride)
	}
}

// TestChatSendCompositeModelRefPinsProvider — a "<provider>/<model>" model value
// (the identity the capability DTO and the pickers hand out) pins the provider
// too, so a caller does not have to send the two fields separately.
func TestChatSendCompositeModelRefPinsProvider(t *testing.T) {
	m, ag := newOverrideChatMethods(t)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	client := gateway.NewTestClient(permissions.RoleAdmin, store.MasterTenantID, "user-1")

	m.handleSend(ctx, client, sendChatParams(t, map[string]any{
		"message": "hello",
		"agentId": "override-agent",
		"model":   "alt-provider/composite-model",
	}))

	req := awaitChatRunRequest(t, ag)
	if req.ModelOverride != "composite-model" {
		t.Fatalf("ModelOverride = %q, want the model half of the reference", req.ModelOverride)
	}
	if req.ProviderOverride == nil || req.ProviderOverride.Name() != "alt-provider" {
		t.Fatalf("ProviderOverride = %v, want the provider half resolved through the registry", req.ProviderOverride)
	}
}

// TestChatSendCompositeModelRefConflictingProviderIsRejected — a model reference
// that names one provider while `provider` names another is a caller mistake, and
// running on either one would be a silent misroute.
func TestChatSendCompositeModelRefConflictingProviderIsRejected(t *testing.T) {
	m, ag := newOverrideChatMethods(t)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	client, frames := gateway.NewCapturingTestClient(permissions.RoleAdmin, store.MasterTenantID, "user-1", 4)

	m.handleSend(ctx, client, sendChatParams(t, map[string]any{
		"message":  "hello",
		"agentId":  "override-agent",
		"model":    "alt-provider/composite-model",
		"provider": "agent-provider",
	}))

	select {
	case raw := <-frames:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.OK {
			t.Fatalf("response ok=true, want an error for the conflicting provider: %s", raw)
		}
		if resp.Error == nil || !strings.Contains(resp.Error.Message, "alt-provider") {
			t.Fatalf("error = %+v, want the conflicting provider named", resp.Error)
		}
		// The message names the provider the reference carries AND the one that was
		// requested; a placeholder/format mismatch here shipped as "%!s(MISSING)".
		if !strings.Contains(resp.Error.Message, "agent-provider") || strings.Contains(resp.Error.Message, "%!") {
			t.Fatalf("error = %q, want both provider names and no format placeholder markers", resp.Error.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat.send did not answer with an error frame")
	}

	select {
	case req := <-ag.requests:
		t.Fatalf("agent loop ran despite the conflicting provider: %+v", req)
	default:
	}
}

// TestChatSendUnknownProviderIsRejected — a provider name nobody registered must
// fail the request instead of silently running on the agent's own provider.
func TestChatSendUnknownProviderIsRejected(t *testing.T) {
	m, ag := newOverrideChatMethods(t)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	client, frames := gateway.NewCapturingTestClient(permissions.RoleAdmin, store.MasterTenantID, "user-1", 4)

	m.handleSend(ctx, client, sendChatParams(t, map[string]any{
		"message":  "hello",
		"agentId":  "override-agent",
		"provider": "nope",
	}))

	select {
	case raw := <-frames:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.OK {
			t.Fatalf("response ok=true, want error for unknown provider: %s", raw)
		}
		if resp.Error == nil || !strings.Contains(resp.Error.Message, "provider not found: nope") {
			t.Fatalf("error = %+v, want provider-not-found for %q", resp.Error, "nope")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat.send did not answer with an error frame")
	}

	select {
	case req := <-ag.requests:
		t.Fatalf("agent loop ran despite unknown provider: %+v", req)
	default:
	}
}
