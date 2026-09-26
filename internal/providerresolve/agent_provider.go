package providerresolve

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// ResolveInputs carries the durable state provider resolution reads on top of
// the registry. The zero value reproduces the pre-phase-5 behaviour: only the
// agent's own fallback chain, with in-memory cooldown.
type ResolveInputs struct {
	// Providers supplies the provider rows: settings.fallback_chain (the
	// provider-level default chain) and the durable cooldown/health state
	// (provider_health). Optional — nil disables both.
	Providers store.ProviderStore
}

// ResolveConfiguredProvider resolves the provider an agent should actually use.
// It applies ChatGPT OAuth routing from the promoted agent routing field when present.
func ResolveConfiguredProvider(registry *providers.Registry, agent *store.AgentData) (providers.Provider, error) {
	if registry == nil || agent == nil {
		return nil, fmt.Errorf("provider registry unavailable")
	}

	baseProvider, baseErr := registry.GetForTenant(agent.TenantID, agent.Provider)
	if baseErr == nil {
		if _, ok := baseProvider.(*providers.CodexProvider); !ok {
			return baseProvider, nil
		}
	}

	var providerDefaults *store.ChatGPTOAuthRoutingConfig
	if codex, ok := baseProvider.(*providers.CodexProvider); ok {
		if defaults := codex.RoutingDefaults(); defaults != nil {
			providerDefaults = &store.ChatGPTOAuthRoutingConfig{
				Strategy:           defaults.Strategy,
				ExtraProviderNames: defaults.ExtraProviderNames,
			}
		}
	}
	if routing := store.ResolveEffectiveChatGPTOAuthRouting(providerDefaults, agent.ParseChatGPTOAuthRouting()); routing != nil {
		router := providers.NewChatGPTOAuthRouter(
			agent.TenantID,
			registry,
			agent.Provider,
			routing.Strategy,
			routing.ExtraProviderNames,
		)
		if router != nil && router.HasRegisteredProviders() {
			return router, nil
		}
	}

	if baseErr == nil {
		return baseProvider, nil
	}
	return nil, baseErr
}

// ResolveAgentProvider resolves the agent runtime provider, including generic
// per-agent model fallback when configured, merged with the primary provider's
// own default chain.
//
// Chain order: the agent's model_fallback candidates come first (the agent wins
// on a duplicate provider/model pair), then the primary provider's
// settings.fallback_chain is appended. The fallback wrapper is built when either
// chain is non-empty; a per-request ProviderOverride/ModelOverride bypasses it by
// construction (the caller resolves the override before this function).
func ResolveAgentProvider(ctx context.Context, registry *providers.Registry, agent *store.AgentData, in ResolveInputs) (providers.Provider, error) {
	baseProvider, err := ResolveConfiguredProvider(registry, agent)
	if err != nil {
		return nil, err
	}
	if registry == nil || agent == nil {
		return baseProvider, nil
	}

	fallbackCfg := agent.ParseModelFallback()
	var agentChain []store.ModelFallbackCandidate
	if fallbackCfg != nil {
		agentChain = fallbackCfg.Candidates
	}
	merged := store.MergeFallbackCandidates(agentChain, providerLevelChain(ctx, in.Providers, agent.Provider))
	if len(merged) == 0 {
		return baseProvider, nil
	}

	candidates := make([]providers.FallbackCandidate, 0, len(merged))
	for _, candidate := range merged {
		provider, err := registry.GetForTenant(agent.TenantID, candidate.Provider)
		if err != nil || provider == nil {
			continue
		}
		candidates = append(candidates, providers.FallbackCandidate{
			ProviderName: candidate.Provider,
			Model:        candidate.Model,
			Provider:     provider,
		})
	}
	if len(candidates) == 0 {
		return baseProvider, nil
	}

	maxAttempts := 0
	cooldownEnabled := true
	if fallbackCfg != nil {
		maxAttempts = fallbackCfg.MaxAttempts
		if fallbackCfg.CooldownEnabled != nil {
			cooldownEnabled = *fallbackCfg.CooldownEnabled
		}
	}
	var cooldowns providers.CooldownStore
	if in.Providers != nil {
		cooldowns = &providerCooldownStore{providers: in.Providers, ids: map[string]uuid.UUID{}}
	}
	return providers.NewModelFallbackProvider(providers.FallbackCandidate{
		ProviderName: agent.Provider,
		Model:        agent.Model,
		Provider:     baseProvider,
	}, candidates, maxAttempts, cooldownEnabled, cooldowns), nil
}

// providerLevelChain reads the provider-level default chain out of the primary
// provider's settings.fallback_chain. An unreadable row or a malformed settings
// blob means "no provider chain": neither may fail agent resolution.
func providerLevelChain(ctx context.Context, ps store.ProviderStore, providerName string) []store.ModelFallbackCandidate {
	if ps == nil || providerName == "" {
		return nil
	}
	row, err := lookupProviderRow(ctx, ps, providerName)
	if err != nil || row == nil {
		return nil
	}
	return store.ParseProviderFallbackChain(row.Settings)
}

// lookupProviderRow reads a provider row by name, falling back to the master
// tenant. An agent may legitimately run on a master-registered provider (the
// registry resolves tenant first, then master) and provider rows are
// configuration, not tenant data — the same fallback usage/caps uses when it
// resolves a provider by name (usage/caps/service.go resolveProvider).
func lookupProviderRow(ctx context.Context, ps store.ProviderStore, name string) (*store.LLMProviderData, error) {
	if ps == nil || strings.TrimSpace(name) == "" {
		return nil, sql.ErrNoRows
	}
	row, err := ps.GetProviderByName(ctx, name)
	if err == nil {
		return row, nil
	}
	if tenantID := store.TenantIDFromContext(ctx); tenantID != uuid.Nil && tenantID != store.MasterTenantID {
		if fallback, fallbackErr := ps.GetProviderByName(store.WithTenantID(ctx, store.MasterTenantID), name); fallbackErr == nil {
			return fallback, nil
		}
	}
	return nil, err
}

// providerCooldownStore adapts store.ProviderStore to the runtime-facing
// providers.CooldownStore: the fallback wrapper only knows provider *names*
// (they are all a FallbackCandidate carries), while provider_health is keyed by
// provider id. Resolved ids are cached per process; a stale id (provider deleted
// and a different row created under the same name) only costs a logged write
// failure, and the next process start re-resolves.
type providerCooldownStore struct {
	providers store.ProviderStore
	mu        sync.Mutex
	ids       map[string]uuid.UUID
}

func (s *providerCooldownStore) providerID(ctx context.Context, name string) (uuid.UUID, error) {
	s.mu.Lock()
	id, ok := s.ids[name]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	row, err := lookupProviderRow(ctx, s.providers, name)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve provider %q for cooldown state: %w", name, err)
	}
	if row == nil {
		return uuid.Nil, fmt.Errorf("resolve provider %q for cooldown state: %w", name, sql.ErrNoRows)
	}
	s.mu.Lock()
	s.ids[name] = row.ID
	s.mu.Unlock()
	return row.ID, nil
}

// CooldownState reads the persisted cooldown state. ok is false when the
// provider has no health row at all (never failed) — a real row always carries
// updated_at, so a zero UpdatedAt is the "no row" signal from the store.
func (s *providerCooldownStore) CooldownState(ctx context.Context, providerName string) (providers.CooldownState, bool, error) {
	id, err := s.providerID(ctx, providerName)
	if err != nil {
		return providers.CooldownState{}, false, err
	}
	health, err := s.providers.GetProviderHealth(ctx, id)
	if err != nil {
		return providers.CooldownState{}, false, err
	}
	if health.UpdatedAt.IsZero() {
		return providers.CooldownState{}, false, nil
	}
	state := providers.CooldownState{ConsecutiveFailures: health.ConsecutiveFailures}
	if health.CooldownUntil != nil {
		state.CooldownUntil = *health.CooldownUntil
	}
	if health.LastProbeAt != nil {
		state.LastProbe = *health.LastProbeAt
	}
	return state, true, nil
}

func (s *providerCooldownStore) RecordFailure(ctx context.Context, providerName, errorClass string, cooldownUntil time.Time) error {
	id, err := s.providerID(ctx, providerName)
	if err != nil {
		return err
	}
	return s.providers.RecordProviderFailure(ctx, id, errorClass, cooldownUntil)
}

func (s *providerCooldownStore) MarkProbe(ctx context.Context, providerName string) error {
	id, err := s.providerID(ctx, providerName)
	if err != nil {
		return err
	}
	return s.providers.MarkProviderProbe(ctx, id)
}

func (s *providerCooldownStore) ClearFailure(ctx context.Context, providerName string) error {
	id, err := s.providerID(ctx, providerName)
	if err != nil {
		return err
	}
	return s.providers.RecordProviderSuccess(ctx, id)
}
