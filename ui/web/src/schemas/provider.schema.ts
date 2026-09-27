import { z } from "zod";
import { isValidSlug } from "@/lib/slug";
import type {
  ModelCapabilities,
  ModelCost,
  ProviderCapability,
  ProviderCapabilityModel,
  ProvidersCapabilitiesResponse,
} from "@/types/provider";

export const providerCreateSchema = z.object({
  name: z
    .string()
    .min(1, "Required")
    .refine(isValidSlug, "Only lowercase letters, numbers, and hyphens"),
  displayName: z.string().optional(),
  providerType: z.string().min(1, "Required"),
  apiBase: z.string().optional(),
  apiKey: z.string().optional(),
  enabled: z.boolean(),
  // ACP-specific fields
  acpBinary: z.string().optional(),
  acpArgs: z.string().optional(),
  acpIdleTTL: z.string().optional(),
  acpPermMode: z.string().optional(),
  acpWorkDir: z.string().optional(),
});

export type ProviderCreateFormData = z.infer<typeof providerCreateSchema>;

// --- GET /v1/providers/capabilities -----------------------------------------
// The capability DTO is the single source of model identity in the UI, so it is
// validated here, once, on the way in from the network. Unknown keys (including
// anything transport-shaped the gateway must never send) are stripped, optional
// fields drop to `undefined`, and a malformed entry is rejected instead of
// reaching a picker.

/**
 * Optional field that tolerates a wrong-typed or null value: it drops to
 * "absent" instead of rejecting the whole provider or model entry. A picker is
 * better off showing a model with an unknown window than hiding the model — and
 * a whole catalogue that is hidden because one scalar was a string is exactly
 * the "looks like no models" failure this parse exists to prevent.
 */
const lenient = <T extends z.ZodType>(schema: T) => schema.optional().catch(undefined);

const costSchema = z.object({
  input: z.number(),
  output: z.number(),
  cache_read: z.number().optional(),
  cache_write: z.number().optional(),
});

const modelCapabilitiesSchema = z.object({
  tool_calling: z.boolean(),
  vision: z.boolean(),
  stream_with_tools: z.boolean(),
  cache_control: z.boolean(),
});

const capabilityModelSchema = z.object({
  id: z.string().min(1),
  label: lenient(z.string()),
  context_window: lenient(z.number().int().nonnegative()),
  max_tokens: lenient(z.number().int().nonnegative()),
  thinking_levels: lenient(z.array(z.string())),
  default_thinking_level: lenient(z.string()),
  supports_fast_mode: lenient(z.boolean()),
  capabilities: lenient(modelCapabilitiesSchema.partial()),
  cost: lenient(costSchema),
  stale: lenient(z.boolean()),
});

const providerCapabilitySchema = z.object({
  id: z.string().min(1),
  provider_id: lenient(z.string()),
  label: lenient(z.string()),
  wire_api: lenient(z.string()),
  auth_kind: lenient(z.string()),
  model_source: lenient(z.string()),
  default_model_id: lenient(z.string()),
  models: lenient(z.array(z.unknown())),
  stale: lenient(z.boolean()),
  last_refreshed_at: lenient(z.string()),
});

/** Wire shape of GET /v1/providers/capabilities. */
export const providersCapabilitiesResponseSchema = z.object({
  providers: z.array(z.unknown()).optional(),
});

function toModelCapabilities(raw: Partial<ModelCapabilities> | undefined): ModelCapabilities {
  return {
    tool_calling: raw?.tool_calling === true,
    vision: raw?.vision === true,
    stream_with_tools: raw?.stream_with_tools === true,
    cache_control: raw?.cache_control === true,
  };
}

function toCapabilityModel(raw: z.infer<typeof capabilityModelSchema>): ProviderCapabilityModel {
  const model: ProviderCapabilityModel = {
    id: raw.id,
    label: raw.label ?? raw.id,
    context_window: raw.context_window ?? 0,
    max_tokens: raw.max_tokens ?? 0,
    supports_fast_mode: raw.supports_fast_mode === true,
    capabilities: toModelCapabilities(raw.capabilities),
    stale: raw.stale === true,
  };
  if (raw.thinking_levels && raw.thinking_levels.length > 0) model.thinking_levels = raw.thinking_levels;
  if (raw.default_thinking_level) model.default_thinking_level = raw.default_thinking_level;
  if (raw.cost) {
    const cost: ModelCost = { input: raw.cost.input, output: raw.cost.output };
    if (raw.cost.cache_read !== undefined) cost.cache_read = raw.cost.cache_read;
    if (raw.cost.cache_write !== undefined) cost.cache_write = raw.cost.cache_write;
    model.cost = cost;
  }
  return model;
}

/**
 * Parse a GET /v1/providers/capabilities body into the typed capability catalogue.
 *
 * Validation is per entry and per model: one malformed provider or one malformed
 * model must not hide the rest of the catalogue (the gateway takes the same
 * stance when one provider's catalogue is unreadable). A body that is not an
 * object at all, or carries no `providers` array, yields an empty catalogue
 * rather than throwing — the caller renders a retry affordance for it, which is
 * the point: a broken catalogue must never look like "this tenant has no models".
 */
export function parseProvidersCapabilitiesResponse(raw: unknown): ProvidersCapabilitiesResponse {
  const envelope = providersCapabilitiesResponseSchema.safeParse(raw);
  if (!envelope.success) return { providers: [] };

  const providers: ProviderCapability[] = [];
  for (const rawProvider of envelope.data.providers ?? []) {
    const entry = providerCapabilitySchema.safeParse(rawProvider);
    if (!entry.success) continue;
    const models: ProviderCapabilityModel[] = [];
    for (const rawModel of entry.data.models ?? []) {
      const model = capabilityModelSchema.safeParse(rawModel);
      if (model.success) models.push(toCapabilityModel(model.data));
    }
    const provider: ProviderCapability = {
      id: entry.data.id,
      provider_id: entry.data.provider_id ?? "",
      label: entry.data.label ?? entry.data.id,
      wire_api: entry.data.wire_api ?? "",
      auth_kind: entry.data.auth_kind ?? "",
      models,
    };
    if (entry.data.model_source) provider.model_source = entry.data.model_source;
    if (entry.data.default_model_id) provider.default_model_id = entry.data.default_model_id;
    if (entry.data.stale !== undefined) provider.stale = entry.data.stale;
    if (entry.data.last_refreshed_at) provider.last_refreshed_at = entry.data.last_refreshed_at;
    providers.push(provider);
  }
  return { providers };
}
