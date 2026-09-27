import { describe, expect, it } from "vitest";
import { parseProvidersCapabilitiesResponse } from "@/schemas/provider.schema";
import {
  bareModelIdForProvider,
  buildCapabilityModelOptions,
  isModelStale,
  isProviderCatalogueStale,
  qualifyModelIdentity,
  splitModelIdentity,
} from "../provider";

// Verbatim body of GET /v1/providers/capabilities as the gateway serializes it
// (internal/http/provider_capabilities.go): model identity is "<provider>/<model>",
// models is always an array, context_window/max_tokens are omitted when the row
// declares none, and no transport field (api_base, exec_path, api_key, settings,
// compat) is representable.
const apiResponse = {
  providers: [
    {
      id: "groq",
      provider_id: "5f0d4a2c-0000-4000-8000-000000000001",
      label: "Groq",
      wire_api: "openai-completions",
      auth_kind: "api_key",
      model_source: "bundled",
      default_model_id: "groq/llama-3.3-70b",
      stale: false,
      last_refreshed_at: "2026-09-26T22:10:00Z",
      models: [
        {
          id: "groq/llama-3.3-70b",
          label: "Llama 3.3 70B",
          context_window: 131072,
          max_tokens: 8192,
          thinking_levels: ["low", "medium", "high"],
          default_thinking_level: "medium",
          capabilities: {
            tool_calling: true,
            vision: false,
            stream_with_tools: true,
            cache_control: false,
          },
          cost: { input: 0.59, output: 0.79 },
          stale: false,
        },
        // A row with no declared window, no price and no display name: all
        // three are optional upstream.
        {
          id: "groq/llama-3.1-8b-instant",
          capabilities: {
            tool_calling: false,
            vision: false,
            stream_with_tools: true,
            cache_control: false,
          },
          stale: false,
        },
      ],
    },
    {
      id: "anthropic",
      provider_id: "5f0d4a2c-0000-4000-8000-000000000002",
      label: "Anthropic",
      wire_api: "anthropic-messages",
      auth_kind: "api_key",
      model_source: "discovered",
      default_model_id: "anthropic/claude-sonnet-4-5-20250929",
      stale: true,
      models: [
        {
          id: "anthropic/claude-sonnet-4-5-20250929",
          label: "Claude Sonnet 4.5",
          context_window: 200000,
          max_tokens: 64000,
          thinking_levels: ["low", "medium", "high"],
          default_thinking_level: "medium",
          capabilities: {
            tool_calling: true,
            vision: true,
            stream_with_tools: true,
            cache_control: true,
          },
          cost: { input: 3, output: 15 },
          stale: true,
        },
      ],
    },
    {
      id: "openrouter",
      provider_id: "5f0d4a2c-0000-4000-8000-000000000003",
      label: "OpenRouter",
      wire_api: "openai-completions",
      auth_kind: "api_key",
      model_source: "bundled",
      stale: false,
      // Vendor id keeps a slash — identity splits on the FIRST slash only.
      models: [
        {
          id: "openrouter/openai/gpt-5.5",
          label: "openai/gpt-5.5",
          context_window: 400000,
          max_tokens: 128000,
          capabilities: {
            tool_calling: true,
            vision: true,
            stream_with_tools: true,
            cache_control: true,
          },
          stale: false,
        },
      ],
    },
    {
      id: "vertex",
      provider_id: "5f0d4a2c-0000-4000-8000-000000000004",
      label: "Vertex AI",
      wire_api: "cli-delegated",
      auth_kind: "service_account",
      model_source: "bundled",
      stale: false,
      models: [],
    },
  ],
  // A transport-shaped key the DTO must never carry, plus a credential.
  api_base: "https://api.groq.com/openai/v1",
  api_key: "sk-should-never-be-read",
};

describe("provider capability DTO contract", () => {
  it("keeps every model id in <provider>/<model> form", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    expect(providers.length).toBe(4);
    for (const provider of providers) {
      for (const model of provider.models) {
        const { provider: prefix, model: bare } = splitModelIdentity(model.id);
        expect(prefix).toBe(provider.id);
        expect(bare.length).toBeGreaterThan(0);
        expect(model.id.startsWith(`${provider.id}/`)).toBe(true);
      }
    }
  });

  it("preserves the stale flags a picker keys its refresh affordance off", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const anthropic = providers.find((p) => p.id === "anthropic");
    const groq = providers.find((p) => p.id === "groq");
    expect(anthropic?.stale).toBe(true);
    expect(groq?.stale).toBe(false);
    expect(isProviderCatalogueStale(anthropic!)).toBe(true);
    expect(isProviderCatalogueStale(groq!)).toBe(false);

    const staleModel = anthropic?.models[0];
    expect(staleModel?.stale).toBe(true);
    expect(isModelStale(staleModel!, anthropic)).toBe(true);
    expect(isModelStale(groq!.models[0]!, groq)).toBe(false);
  });

  it("preserves the capability flags, including the false ones", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const groq = providers.find((p) => p.id === "groq");
    expect(groq?.models[0]?.capabilities).toEqual({
      tool_calling: true,
      vision: false,
      stream_with_tools: true,
      cache_control: false,
    });
    const anthropic = providers.find((p) => p.id === "anthropic");
    expect(anthropic?.models[0]?.capabilities.cache_control).toBe(true);
    expect(anthropic?.models[0]?.capabilities.vision).toBe(true);
  });

  it("keeps the window, price and thinking metadata the picker shows", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const groq = providers.find((p) => p.id === "groq");
    const [full, sparse] = groq!.models;
    expect(full?.context_window).toBe(131072);
    expect(full?.max_tokens).toBe(8192);
    expect(full?.thinking_levels).toEqual(["low", "medium", "high"]);
    expect(full?.default_thinking_level).toBe("medium");
    expect(full?.cost).toEqual({ input: 0.59, output: 0.79 });
    expect(full?.label).toBe("Llama 3.3 70B");
    // Omitted optional fields degrade instead of vanishing into NaN.
    expect(sparse?.context_window).toBe(0);
    expect(sparse?.max_tokens).toBe(0);
    expect(sparse?.cost).toBeUndefined();
    // A label the gateway never sent falls back to the id, never to "".
    expect(sparse?.label).toBe("groq/llama-3.1-8b-instant");
  });

  it("keeps the provider dispatch identity (wire_api/auth_kind/model_source)", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    expect(providers.map((p) => [p.id, p.wire_api, p.auth_kind, p.model_source])).toEqual([
      ["groq", "openai-completions", "api_key", "bundled"],
      ["anthropic", "anthropic-messages", "api_key", "discovered"],
      ["openrouter", "openai-completions", "api_key", "bundled"],
      ["vertex", "cli-delegated", "service_account", "bundled"],
    ]);
    const groq = providers.find((p) => p.id === "groq");
    expect(groq?.default_model_id).toBe("groq/llama-3.3-70b");
    expect(groq?.last_refreshed_at).toBe("2026-09-26T22:10:00Z");
    expect(groq?.provider_id).toBe("5f0d4a2c-0000-4000-8000-000000000001");
  });

  it("never carries transport or credential fields into the picker", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    for (const provider of providers) {
      for (const leaked of ["api_base", "api_key", "exec_path", "settings", "compat"]) {
        expect(leaked in provider).toBe(false);
      }
      for (const model of provider.models) {
        for (const leaked of ["api_base", "api_key", "exec_path", "wire_api"]) {
          expect(leaked in model).toBe(false);
        }
      }
    }
  });

  it("keeps an empty catalogue empty rather than inventing a model", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const vertex = providers.find((p) => p.id === "vertex");
    expect(vertex?.models).toEqual([]);
  });
});

describe("provider capability DTO degradation", () => {
  it("returns an empty catalogue for a body that is not a capability response", () => {
    expect(parseProvidersCapabilitiesResponse(null).providers).toEqual([]);
    expect(parseProvidersCapabilitiesResponse("nope").providers).toEqual([]);
    expect(parseProvidersCapabilitiesResponse({}).providers).toEqual([]);
    expect(parseProvidersCapabilitiesResponse({ providers: null }).providers).toEqual([]);
    expect(parseProvidersCapabilitiesResponse({ error: "boom" }).providers).toEqual([]);
  });

  it("tolerates a wrong-typed scalar instead of hiding the model", () => {
    const { providers } = parseProvidersCapabilitiesResponse({
      providers: [
        { id: "groq", models: [{ id: "groq/m", capabilities: { tool_calling: true } }] },
        { label: "no id at all" },
        "not an object",
        { id: "odd-window", models: [{ id: "odd-window/m", context_window: null, max_tokens: "8192" }] },
      ],
    });
    // Only the entries without a usable id are dropped.
    expect(providers.map((p) => p.id)).toEqual(["groq", "odd-window"]);
    expect(providers[0]?.models.map((m) => m.id)).toEqual(["groq/m"]);
    // The odd scalars degrade to "unknown" and the model stays selectable.
    expect(providers[1]?.models.map((m) => m.id)).toEqual(["odd-window/m"]);
    expect(providers[1]?.models[0]?.context_window).toBe(0);
    expect(providers[1]?.models[0]?.max_tokens).toBe(0);
  });

  it("drops one malformed model without hiding its siblings", () => {
    const { providers } = parseProvidersCapabilitiesResponse({
      providers: [
        {
          id: "groq",
          models: [
            { id: "groq/ok", capabilities: { tool_calling: true, vision: true, stream_with_tools: true, cache_control: true } },
            { id: "" },
            { capabilities: { tool_calling: true, vision: true, stream_with_tools: true, cache_control: true } },
          ],
        },
      ],
    });
    expect(providers[0]?.models.map((m) => m.id)).toEqual(["groq/ok"]);
  });

  it("treats a non-boolean stale flag as absent rather than as stale", () => {
    const { providers } = parseProvidersCapabilitiesResponse({
      providers: [{ id: "groq", stale: "yes", models: [{ id: "groq/m", stale: "yes" }] }],
    });
    expect(providers[0]?.stale).toBeUndefined();
    expect(providers[0]?.models[0]?.stale).toBe(false);
    expect(isProviderCatalogueStale(providers[0]!)).toBe(false);
  });
});

describe("model identity mapping", () => {
  it("qualifies a bare model id with the provider name", () => {
    expect(qualifyModelIdentity("groq", "llama-3.3-70b")).toBe("groq/llama-3.3-70b");
    // A vendor id with a slash is still bare — the provider prefix decides.
    expect(qualifyModelIdentity("openrouter", "openai/gpt-5.5")).toBe("openrouter/openai/gpt-5.5");
    expect(qualifyModelIdentity("openrouter", "openrouter/openai/gpt-5.5")).toBe("openrouter/openai/gpt-5.5");
    expect(qualifyModelIdentity("groq", "")).toBe("");
    expect(qualifyModelIdentity("", "llama-3.3-70b")).toBe("llama-3.3-70b");
  });

  it("splits an identity on the first slash so vendor ids survive", () => {
    expect(splitModelIdentity("groq/llama-3.3-70b")).toEqual({ provider: "groq", model: "llama-3.3-70b" });
    expect(splitModelIdentity("openrouter/openai/gpt-5.5")).toEqual({
      provider: "openrouter",
      model: "openai/gpt-5.5",
    });
    // A bare id has no provider half — the caller's context supplies it.
    expect(splitModelIdentity("llama-3.3-70b", "groq")).toEqual({ provider: "groq", model: "llama-3.3-70b" });
    expect(splitModelIdentity("")).toEqual({ provider: "", model: "" });
  });

  it("reduces a catalogue identity to the bare id the transport expects", () => {
    expect(bareModelIdForProvider("groq", "groq/llama-3.3-70b")).toBe("llama-3.3-70b");
    // Another provider's identity, a vendor id and a custom value stay untouched.
    expect(bareModelIdForProvider("groq", "openrouter/openai/gpt-5.5")).toBe("openrouter/openai/gpt-5.5");
    expect(bareModelIdForProvider("groq", "llama-3.3-70b")).toBe("llama-3.3-70b");
    expect(bareModelIdForProvider("groq", "some/custom/thing")).toBe("some/custom/thing");
  });

  it("round-trips every model of a parsed catalogue through the chat.send identity", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    for (const provider of providers) {
      for (const model of provider.models) {
        // What the picker sends to chat.send.
        expect(model.id).toBe(qualifyModelIdentity(provider.id, bareModelIdForProvider(provider.id, model.id)));
        // What a provider that only declares provider_type keeps sending upstream.
        expect(bareModelIdForProvider(provider.id, model.id)).toBe(splitModelIdentity(model.id).model);
      }
    }
  });
});

describe("model picker options", () => {
  it("labels every option with the qualified identity and emits the bare id", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const openrouter = providers.find((p) => p.id === "openrouter")!;
    const options = buildCapabilityModelOptions(openrouter.id, openrouter.models);
    expect(options).toEqual([
      { value: "openai/gpt-5.5", label: "openai/gpt-5.5 (openrouter/openai/gpt-5.5)" },
    ]);
    // The value a config surface stores never carries the provider prefix.
    for (const option of options) {
      expect(option.value.startsWith(`${openrouter.id}/`)).toBe(false);
      expect(option.label).toContain(`${openrouter.id}/${option.value}`);
    }
  });

  it("keeps the label as the plain model id when the gateway sent no display name", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const groq = providers.find((p) => p.id === "groq")!;
    const options = buildCapabilityModelOptions(groq.id, groq.models);
    expect(options).toEqual([
      { value: "llama-3.3-70b", label: "Llama 3.3 70B (groq/llama-3.3-70b)" },
      { value: "llama-3.1-8b-instant", label: "groq/llama-3.1-8b-instant" },
    ]);
  });

  it("filters on the bare id or the identity, and prepends deduped extras", () => {
    const { providers } = parseProvidersCapabilitiesResponse(apiResponse);
    const groq = providers.find((p) => p.id === "groq")!;
    expect(buildCapabilityModelOptions(groq.id, groq.models, { modelFilter: "70b" }).map((o) => o.value)).toEqual([
      "llama-3.3-70b",
    ]);
    expect(
      buildCapabilityModelOptions(groq.id, groq.models, { modelFilter: "groq/llama-3.1" }).map((o) => o.value),
    ).toEqual(["llama-3.1-8b-instant"]);

    // Extras are prepended, and one that duplicates an already listed model is
    // dropped rather than offered twice.
    expect(
      buildCapabilityModelOptions(groq.id, groq.models, {
        extraModels: [
          { id: "llama-3.3-70b", name: "Duplicate of a catalogue model" },
          { id: "nomic-embed-text", name: "Nomic Embed" },
        ],
      }).map((o) => o.value),
    ).toEqual(["nomic-embed-text", "llama-3.3-70b", "llama-3.1-8b-instant"]);

    // A curated extra survives a filter no catalogue model matches: that is the
    // embedding-picker case the extras exist for.
    expect(
      buildCapabilityModelOptions(groq.id, groq.models, {
        modelFilter: "embed",
        extraModels: [{ id: "nomic-embed-text", name: "Nomic Embed" }],
      }),
    ).toEqual([{ value: "nomic-embed-text", label: "Nomic Embed" }]);
  });
});
