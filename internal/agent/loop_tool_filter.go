package agent

import (
	"slices"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/providers/discovery"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// imageGenToolDef is the native image_generation tool sentinel. The request
// builder keys off Type (Codex emits a bare {"type":"image_generation"} object,
// no function wrapper). Function is populated with a name-only schema so the many
// pipeline/provider sites that read td.Function.Name (think_stage allowlist,
// shouldRetryTaskMCP, history tool names, non-codex request builders) never
// nil-deref — the v3.14.0 crash was one such site.
var imageGenToolDef = providers.ToolDefinition{
	Type:     "image_generation",
	Function: &providers.ToolFunctionSchema{Name: "image_generation"},
}

func (l *Loop) toolVisibleForChannel(name, channelType string, telegramManagerPermissions []string) bool {
	if name == "telegram_manager" {
		return channelType == "telegram" && len(telegramManagerPermissions) > 0
	}
	if l.tools == nil {
		return true
	}
	tool, ok := l.tools.Get(name)
	if !ok {
		return true
	}
	ca, ok := tool.(tools.ChannelAware)
	if !ok {
		return true
	}
	if channelType == "" {
		return false
	}
	return slices.Contains(ca.RequiredChannelTypes(), channelType)
}

// modelCapabilityLookup is the request path's per-model capability source: it
// answers what a catalogue row (llm_models.capabilities) declares for a
// (provider, model) pair. The default is the shipped discovery snapshot — the
// same table the catalogue seeds into llm_models rows — so a seeded row and the
// capability the request path applies can never disagree. A deployment that
// reads operator-edited rows installs its own store-backed lookup here, exactly
// like ModelCostResolver on the registry.
var modelCapabilityLookup providers.ModelCapabilityLookup = bundledModelCapabilityLookup

// bundledModelCapabilityLookup adapts the shipped snapshot to the lookup shape.
// The snapshot is keyed by provider type (never by the user-chosen name), which
// is why the lookup carries the type as well as the name.
func bundledModelCapabilityLookup(_, providerType, model string) (providers.ModelCapabilityOverride, bool) {
	return discovery.BundledModelCapabilities(providerType, model)
}

// runProvider returns the provider THIS run will call: ProviderOverride wins,
// otherwise a ModelOverride unwraps the fallback wrapper to its primary so
// capability probing sees the transport that actually serves the request.
func (l *Loop) runProvider(req *RunRequest) providers.Provider {
	if req != nil && req.ProviderOverride != nil {
		return req.ProviderOverride
	}
	provider := l.provider
	if req != nil && req.ModelOverride != "" {
		if fallback, ok := provider.(interface{ PrimaryProvider() providers.Provider }); ok {
			provider = fallback.PrimaryProvider()
		}
	}
	return provider
}

// effectiveModel returns the model THIS run will request: ModelOverride wins
// over the agent's configured model.
func (l *Loop) effectiveModel(req *RunRequest) string {
	if req != nil && req.ModelOverride != "" {
		return req.ModelOverride
	}
	return l.model
}

// resolveCapabilities returns the capabilities that govern this run's request:
// the provider's declared capabilities overlaid with the catalogue row's
// per-model override, plus the row's declared context window.
//
// ProviderDeclared is false when the transport does not implement
// CapabilitiesAware (an unknown/legacy provider): consumers must then apply no
// gating at all — "undeclared" is not "unsupported".
func (l *Loop) resolveCapabilities(req *RunRequest) providers.ModelCapabilityResolution {
	return l.capabilitiesFor(l.runProvider(req), l.effectiveModel(req))
}

// capabilitiesFor resolves the same override for an explicit provider+model pair
// — the request path uses it with the provider the pipeline will actually call
// (RunState.Provider), which an override or a routed run can differ from
// l.provider.
func (l *Loop) capabilitiesFor(provider providers.Provider, model string) providers.ModelCapabilityResolution {
	if provider == nil {
		return providers.ModelCapabilityResolution{}
	}
	aware, ok := provider.(providers.CapabilitiesAware)
	if !ok {
		return providers.ModelCapabilityResolution{}
	}
	providerType := ""
	if ta, ok := provider.(providers.ProviderTypeAware); ok {
		providerType = ta.ProviderType()
	}
	return providers.ResolveModelCapabilities(
		aware.Capabilities(),
		modelCapabilityLookup,
		provider.Name(),
		providerType,
		model,
	)
}

// toolsUnavailableNotice tells the model — in-band, not silently — that the
// current model cannot call tools. English-only, matching the other loop
// hints (LLM consumption).
func toolsUnavailableNotice(model string) providers.Message {
	name := model
	if name == "" {
		name = "the selected model"
	}
	return providers.Message{
		Role: "user",
		Content: "[System] Tools are unavailable for " + name +
			": its catalogue row declares that this model does not support tool calling. " +
			"Answer with text only — do not request tool calls.",
	}
}

// buildFilteredTools resolves the per-iteration tool definitions based on policy,
// disabled tools, bootstrap mode, skill visibility, channel type, and iteration budget.
// Per-user MCP tools (require_user_credentials servers) are passed in via userTools —
// their objects deliberately live ONLY in l.mcpUserTools (cross-user isolation), NOT
// in the shared registry. They are surfaced via a request-scoped overlay registry so
// the PolicyEngine evaluates AND emits them under the SAME allow/deny rules as
// registry tools; execution routes per-actor via executeToolForActor.
// Returns tool definitions for the provider, an allowed-tools map for execution validation,
// and the (potentially modified) messages slice when final-iteration stripping appends a hint.
func (l *Loop) buildFilteredTools(req *RunRequest, hadBootstrap bool, iteration, maxIter int, messages []providers.Message, userTools []tools.Tool) ([]providers.ToolDefinition, map[string]bool, []providers.Message) {
	// Capability gate (phase 5): a model whose catalogue row declares
	// tool_calling=false must not receive tool schemas at all, and must be told
	// why — a silent empty tool list makes the model invent tool calls it cannot
	// execute. The allowlist is empty (never nil): nil means "no restriction" to
	// AuthorizeToolCall, so an empty map is what actually fails closed.
	caps := l.resolveCapabilities(req)
	if caps.ProviderDeclared && !caps.Capabilities.ToolCalling {
		if iteration != maxIter {
			// The per-run cache replays this result for iterations 0..maxIter-1;
			// only the final iteration would replay the notice, and by then the
			// model has already been told.
			messages = append(messages, toolsUnavailableNotice(l.effectiveModel(req)))
		}
		return nil, map[string]bool{}, messages
	}

	// Build provider request with policy-filtered tools.
	var toolDefs []providers.ToolDefinition
	var allowedTools map[string]bool
	if l.toolPolicy != nil {
		// Per-user MCP tool objects are NOT in the shared registry (cross-user
		// isolation — see getUserMCPTools), so FilterTools alone cannot emit them.
		// Wrap the registry in a request-scoped overlay that adds the calling actor's
		// per-user tools: FilterTools then evaluates them through the same policy
		// pipeline (profile/allow/deny, incl. an explicit deny on a per-user tool name)
		// and emits them via overlay.Get. The overlay is local and discarded after this
		// call, so the shared registry — and thus other users — stay unaffected.
		// NewUserToolOverlay returns l.tools unchanged when userTools is empty.
		registry := tools.NewUserToolOverlay(l.tools, userTools)
		toolDefs = l.toolPolicy.FilterTools(registry, l.id, l.provider.Name(), l.agentToolPolicy, req.ToolAllow, false, false)
		allowedTools = make(map[string]bool, len(toolDefs))
		for _, td := range toolDefs {
			if td.Function != nil {
				allowedTools[td.Function.Name] = true
			}
		}
	} else {
		// No policy → all tools allowed. ProviderDefs() omits per-user MCP tools (not in
		// the shared registry), so append their defs directly (dedup by name).
		toolDefs = l.tools.ProviderDefs()
		if len(userTools) > 0 {
			seen := make(map[string]bool, len(toolDefs))
			for _, td := range toolDefs {
				if td.Function != nil {
					seen[td.Function.Name] = true
				}
			}
			for _, t := range userTools {
				if t == nil || seen[t.Name()] {
					continue
				}
				seen[t.Name()] = true
				toolDefs = append(toolDefs, tools.ToProviderDef(t))
			}
		}
	}

	// V3 orchestration mode filtering: hide tools the agent shouldn't see.
	// spawn: no delegate/team_tasks. delegate: no team_tasks. team: all.
	if orchDeny := orchModeDenyTools(l.orchMode); len(orchDeny) > 0 {
		filtered := toolDefs[:0:0]
		for _, td := range toolDefs {
			if td.Function == nil || !orchDeny[td.Function.Name] {
				filtered = append(filtered, td)
			} else {
				if allowedTools != nil {
					delete(allowedTools, td.Function.Name)
				}
			}
		}
		toolDefs = filtered
	}

	// Per-tenant tool exclusions: remove tools disabled for this agent's tenant.
	if len(l.disabledTools) > 0 {
		filtered := toolDefs[:0]
		for _, td := range toolDefs {
			if td.Function == nil || !l.disabledTools[td.Function.Name] {
				filtered = append(filtered, td)
			} else {
				if allowedTools != nil {
					delete(allowedTools, td.Function.Name)
				}
			}
		}
		toolDefs = filtered
	}

	// Bootstrap mode: restrict API tool definitions to write_file only (open agents).
	// Predefined agents keep all tools — BOOTSTRAP.md guides behavior.
	if hadBootstrap && l.agentType != store.AgentTypePredefined {
		var bootstrapDefs []providers.ToolDefinition
		for _, td := range toolDefs {
			if td.Function != nil && bootstrapToolAllowlist[td.Function.Name] {
				bootstrapDefs = append(bootstrapDefs, td)
			}
		}
		toolDefs = bootstrapDefs
	}

	// Hide skill_manage from LLM when skill_evolve is off.
	// Tool stays in the registry (shared) but won't appear in API tool definitions.
	if !l.skillEvolve {
		filtered := toolDefs[:0:0]
		for _, td := range toolDefs {
			if td.Function == nil || td.Function.Name != "skill_manage" {
				filtered = append(filtered, td)
			}
		}
		toolDefs = filtered
	}

	// Hide channel-specific tools when channel type doesn't match. Channel-aware
	// tools are hidden when there is no channel context; telegram_manager also
	// requires explicit channel permissions before it is visible to the LLM.
	filtered := toolDefs[:0:0]
	for _, td := range toolDefs {
		if td.Function != nil && !l.toolVisibleForChannel(td.Function.Name, req.ChannelType, req.TelegramManagerPermissions) {
			if allowedTools != nil {
				delete(allowedTools, td.Function.Name)
			}
			continue
		}
		filtered = append(filtered, td)
	}
	toolDefs = filtered

	// Final iteration: strip all tools to force a text-only response.
	// Without this the model may keep requesting tools and exit with "...".
	if iteration == maxIter {
		toolDefs = nil
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: "[System] Final iteration reached. Summarize all findings and respond to the user now. No more tool calls allowed.",
		})
		return toolDefs, allowedTools, messages
	}

	// Two-tier image generation gate:
	//   (1) provider supports native image_generation (ImageGeneration capability,
	//       including any per-model catalogue override)
	//   (2) agent config allows it (allowImageGeneration — defaults true, set false via
	//       other_config.allow_image_generation = false in the admin agent configuration)
	if l.allowImageGeneration && caps.Capabilities.ImageGeneration {
		toolDefs = append(toolDefs, imageGenToolDef)
	}

	return toolDefs, allowedTools, messages
}
