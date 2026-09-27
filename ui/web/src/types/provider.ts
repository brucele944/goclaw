import type {
  ChatGPTOAuthRoutingConfig,
  EffectiveChatGPTOAuthRoutingStrategy,
} from "./agent";

export interface ProviderData {
  id: string;
  name: string;
  display_name: string;
  provider_type: string;
  /**
   * Transport family the reworked provider subsystem dispatches on, e.g.
   * "openai-completions" | "anthropic-messages" | "cli-delegated".
   * Absent on gateways that predate the rework — render nothing then.
   */
  wire_api?: string;
  /** How the provider authenticates; see AuthKind. */
  auth_kind?: string;
  /** Where the model catalogue came from: "bundled" | "discovered" (opaque; see ModelSource). */
  model_source?: string;
  api_base: string;
  api_key: string; // masked "***" from server
  enabled: boolean;
  settings?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ProviderInput {
  name: string;
  display_name?: string;
  provider_type: string;
  api_base?: string;
  api_key?: string;
  enabled?: boolean;
  settings?: Record<string, unknown>;
}

// ---------------------------------------------------------------------------
// Provider capability catalogue — GET /v1/providers/capabilities
//
// The gateway builds this DTO server-side so every surface reads the same model
// identity: "<provider>/<model-id>". The provider segment is the provider *name*
// (the same slug GET /v1/models and chat.send use), and the model segment may
// itself contain slashes (OpenRouter-style "openai/gpt-5.5"), so an identity is
// always split on the FIRST slash and never on the last.
// ---------------------------------------------------------------------------

/** Transport family of a provider. Unknown values stay opaque (forwarded as-is). */
export type WireAPI =
  | "openai-completions"
  | "anthropic-messages"
  | "cli-delegated"
  | (string & {});

/** Credential kind of a provider. Unknown values stay opaque (forwarded as-is). */
export type AuthKind =
  | "api_key"
  | "oauth_browser"
  | "service_account"
  | "cli_delegated"
  | "none"
  | (string & {});

/** Provenance of a provider's model catalogue. "stale" marks a catalogue the
 *  gateway could not refresh (see `isProviderCatalogueStale`). */
export type ModelSource = "bundled" | "discovered" | "stale" | (string & {});

/** Runtime traits the pipeline actually consumes for a model. */
export interface ModelCapabilities {
  tool_calling: boolean;
  vision: boolean;
  stream_with_tools: boolean;
  cache_control: boolean;
}

/** Per-1M-token price set. */
export interface ModelCost {
  input: number;
  output: number;
  cache_read?: number;
  cache_write?: number;
}

/** One model of a provider's capability catalogue. */
export interface ProviderCapabilityModel {
  /** Canonical identity: "<provider>/<model-id>". */
  id: string;
  label: string;
  context_window: number;
  max_tokens: number;
  thinking_levels?: string[];
  default_thinking_level?: string;
  supports_fast_mode: boolean;
  capabilities: ModelCapabilities;
  cost?: ModelCost;
  /**
   * True when the gateway is serving a cached copy because the last discovery
   * refresh failed. Non-blocking: the model is still usable, but the UI must say
   * so and offer a refresh.
   */
  stale: boolean;
}

/** One provider of the capability catalogue. */
export interface ProviderCapability {
  /** Provider name slug — the "<provider>" half of every model identity. */
  id: string;
  provider_id: string;
  label: string;
  wire_api: WireAPI;
  auth_kind: AuthKind;
  model_source?: ModelSource;
  default_model_id?: string;
  models: ProviderCapabilityModel[];
  /**
   * True when the gateway is serving a cached catalogue: the fingerprint moved
   * or the TTL expired, so the listing may not describe the current upstream.
   * Each model repeats it in its own `stale` flag.
   */
  stale?: boolean;
  /** Newest catalogue fetch, when discovery ever ran. */
  last_refreshed_at?: string;
}

/** Body of GET /v1/providers/capabilities. */
export interface ProvidersCapabilitiesResponse {
  providers: ProviderCapability[];
}

/**
 * Build the canonical "<provider>/<model-id>" identity.
 *
 * A value already carrying *this* provider's prefix is returned untouched. A
 * bare value is prefixed even when it contains slashes, because vendor model ids
 * legitimately do ("openai/gpt-5.5" on OpenRouter) — comparing against the
 * provider prefix is the only test that tells those apart. Use
 * `splitModelIdentity` to read an identity back.
 */
export function qualifyModelIdentity(provider: string, modelId: string): string {
  const model = modelId.trim();
  if (!model) return "";
  const providerName = provider.trim();
  if (!providerName) return model;
  return model.startsWith(`${providerName}/`) ? model : `${providerName}/${model}`;
}

/**
 * Split an identity into its provider and model halves.
 *
 * Splits on the FIRST slash: the provider half is a name slug (never contains
 * "/"), the model half keeps the rest verbatim, so "openrouter/openai/gpt-5.5"
 * parses as provider "openrouter".
 *
 * @param fallbackProvider used for a slash-less value. A value containing a
 * slash is always read as a qualified identity — nothing can tell a bare vendor
 * id from another provider's identity, which is why callers holding a bare id
 * compare against the provider prefix (`bareModelIdForProvider`) instead.
 */
export function splitModelIdentity(
  identity: string,
  fallbackProvider = "",
): { provider: string; model: string } {
  const id = identity.trim();
  if (!id) return { provider: fallbackProvider.trim(), model: "" };
  const slash = id.indexOf("/");
  if (slash <= 0) return { provider: fallbackProvider.trim(), model: id };
  return { provider: id.slice(0, slash), model: id.slice(slash + 1) };
}

/**
 * Reduce a catalogue identity to the bare model id a provider expects upstream.
 *
 * Config surfaces (agent `model`, embedding model, compaction model, …) keep the
 * provider in its own field and hand the transport a bare model id, so a value
 * qualified with the *same* provider is normalized back. An identity qualified
 * with another provider — or a plain custom value the operator typed — is passed
 * through unchanged rather than mangled.
 */
export function bareModelIdForProvider(provider: string, value: string): string {
  const { provider: prefix, model } = splitModelIdentity(value);
  if (prefix && model && prefix === provider.trim()) return model;
  return value.trim();
}

/**
 * True when the provider's catalogue is a cached copy rather than a fresh
 * listing.
 *
 * The DTO reports this as a boolean per provider and per model; a `model_source`
 * of "stale" is honoured too so a gateway that only encodes it there still
 * triggers the affordance.
 */
export function isProviderCatalogueStale(
  provider: Pick<ProviderCapability, "model_source"> & Partial<Pick<ProviderCapability, "stale">>,
): boolean {
  return provider.stale === true || (provider.model_source ?? "").trim().toLowerCase() === "stale";
}

/** True when this specific model is served from a stale cache. */
export function isModelStale(
  model: Pick<ProviderCapabilityModel, "stale">,
  provider?: Pick<ProviderCapability, "model_source"> & Partial<Pick<ProviderCapability, "stale">>,
): boolean {
  return model.stale === true || (provider ? isProviderCatalogueStale(provider) : false);
}

/** One option of a model picker: the bare id it emits, the identity it shows. */
export interface ModelSelectOption {
  /**
   * Bare model id — what a config surface stores and what the transport sends
   * upstream, with the provider kept in its own sibling field.
   */
  value: string;
  /** The "<provider>/<model>" identity the operator reads, plus the display label. */
  label: string;
}

/**
 * Build a provider's model options from its capability catalogue.
 *
 * The label carries the qualified identity rather than the display name alone:
 * two providers routinely advertise the same label, and the model the operator
 * picks is the one chat.send addresses as "<provider>/<model>".
 *
 * `modelFilter` matches the bare id or the label (e.g. "embed" for an embedding
 * picker); `extraModels` are prepended for curated models the catalogue does not
 * hold, and an extra whose id is already listed is dropped rather than shown
 * twice.
 */
export function buildCapabilityModelOptions(
  provider: string,
  models: readonly ProviderCapabilityModel[],
  options: { modelFilter?: string; extraModels?: readonly { id: string; name: string }[] } = {},
): ModelSelectOption[] {
  let list: ModelSelectOption[] = models.map((model) => {
    const bare = splitModelIdentity(model.id, provider).model || model.id;
    return {
      value: bare,
      label: model.label && model.label !== model.id ? `${model.label} (${model.id})` : model.id,
    };
  });

  const filter = options.modelFilter?.toLowerCase();
  if (filter) {
    list = list.filter(
      (option) => option.value.toLowerCase().includes(filter) || option.label.toLowerCase().includes(filter),
    );
  }

  if (options.extraModels?.length) {
    const listed = new Set(list.map((option) => option.value));
    const extras = options.extraModels
      .filter((extra) => !listed.has(extra.id))
      .map((extra) => ({ value: extra.id, label: extra.name }));
    list = [...extras, ...list];
  }

  return list;
}

export interface ProviderReasoningDefaults {
  effort?: string;
  fallback?: "downgrade" | "provider_default" | "off";
}

export interface ReasoningCapability {
  levels?: string[];
  default_effort?: string;
}

export interface EmbeddingSettings {
  enabled: boolean;
  model?: string;
  api_base?: string;
  dimensions?: number; // truncate output to N dims (e.g. 1536); 0/undefined = model default
}

export interface NormalizedChatGPTOAuthProviderRouting {
  strategy: EffectiveChatGPTOAuthRoutingStrategy;
  extraProviderNames: string[];
}

/** Extract embedding settings from provider.settings */
export function getEmbeddingSettings(settings?: Record<string, unknown>): EmbeddingSettings | null {
  if (!settings?.embedding) return null;
  return settings.embedding as EmbeddingSettings;
}

function normalizeProviderNames(names: unknown): string[] {
  if (!Array.isArray(names)) return [];
  return Array.from(
    new Set(
      names
        .filter((name): name is string => typeof name === "string")
        .map((name) => name.trim())
        .filter(Boolean),
    ),
  );
}

export function normalizeChatGPTOAuthStrategy(
  strategy: unknown,
): EffectiveChatGPTOAuthRoutingStrategy {
  if (strategy === "round_robin") return "round_robin";
  return "priority_order";
}

export function normalizeReasoningEffort(value: unknown): string {
  if (typeof value !== "string") return "";
  const normalized = value.trim().toLowerCase();
  return [
    "off", "auto", "none", "minimal", "low", "medium", "high", "xhigh",
  ].includes(normalized) ? normalized : "";
}

export function normalizeReasoningFallback(
  value: unknown,
): "downgrade" | "provider_default" | "off" {
  if (value === "provider_default" || value === "off") {
    return value;
  }
  return "downgrade";
}

/** Maps advanced reasoning effort levels to the legacy three-tier thinking_level. */
export function deriveLegacyThinkingLevel(effort: string): string {
  switch (effort) {
    case "low":
    case "medium":
    case "high":
      return effort;
    case "minimal":
      return "low";
    case "xhigh":
      return "high";
    default:
      return "off";
  }
}

export function getProviderReasoningDefaults(
  settings?: Record<string, unknown>,
): ProviderReasoningDefaults | null {
  const raw = settings?.reasoning_defaults;
  if (!raw || typeof raw !== "object") return null;
  const reasoning = raw as Record<string, unknown>;
  const effort = normalizeReasoningEffort(reasoning.effort) || "off";
  const fallback = normalizeReasoningFallback(reasoning.fallback);
  if (effort === "off" && fallback === "downgrade") {
    return null;
  }
  return { effort, fallback };
}

export function getChatGPTOAuthProviderRouting(
  settings?: Record<string, unknown>,
): NormalizedChatGPTOAuthProviderRouting | null {
  const rawPool = settings?.codex_pool;
  if (!rawPool || typeof rawPool !== "object") return null;
  const pool = rawPool as Record<string, unknown>;
  const strategy = normalizeChatGPTOAuthStrategy(pool.strategy);
  const extraProviderNames = normalizeProviderNames(pool.extra_provider_names);
  if (strategy === "priority_order" && extraProviderNames.length === 0) {
    return null;
  }
  return {
    strategy,
    extraProviderNames,
  };
}

export function buildProviderSettingsWithChatGPTOAuthRouting(
  settings: Record<string, unknown> | undefined,
  routing: ChatGPTOAuthRoutingConfig,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(settings ?? {}) };
  const strategy = normalizeChatGPTOAuthStrategy(routing.strategy);
  const extraProviderNames = normalizeProviderNames(routing.extra_provider_names);

  delete next.codex_pool;
  if (extraProviderNames.length > 0) {
    next.codex_pool = {
      strategy,
      extra_provider_names: extraProviderNames,
    };
  }

  return next;
}

export function buildProviderSettingsWithReasoningDefaults(
  settings: Record<string, unknown> | undefined,
  reasoning: ProviderReasoningDefaults | null,
): Record<string, unknown> {
  const next: Record<string, unknown> = { ...(settings ?? {}) };
  const effort = normalizeReasoningEffort(reasoning?.effort) || "off";
  const fallback = normalizeReasoningFallback(reasoning?.fallback);

  delete next.reasoning_defaults;
  if (effort !== "off" || fallback !== "downgrade") {
    next.reasoning_defaults = {
      effort,
      fallback,
    };
  }

  return next;
}
