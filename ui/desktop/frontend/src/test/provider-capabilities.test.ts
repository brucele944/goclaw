import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import {
  isCatalogueStale,
  modelIdentity,
  modelOptionsFor,
  parseProviderCapabilities,
  providerOptionsFrom,
} from '../api/provider-capabilities'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('../lib/api', () => ({ getApiClient: () => ({ get }) }))

// Imported after the mock declaration above (vi.mock is hoisted).
import { useProviderCapabilities } from '../hooks/use-provider-capabilities'

/** Frozen gateway contract: GET /v1/providers/capabilities */
const CAPABILITIES_DTO = {
  providers: [
    {
      id: 'groq',
      provider_id: '11111111-2222-3333-4444-555555555555',
      label: 'Groq',
      wire_api: 'openai-completions',
      auth_kind: 'api_key',
      model_source: 'bundled',
      default_model_id: 'groq/llama-3.3-70b',
      models: [
        {
          id: 'groq/llama-3.3-70b',
          label: 'Llama 3.3 70B',
          context_window: 131072,
          max_tokens: 8192,
          thinking_levels: ['low', 'medium', 'high'],
          default_thinking_level: 'medium',
          supports_fast_mode: false,
          capabilities: { tool_calling: true, vision: false, stream_with_tools: true, cache_control: false },
          cost: { input: 0.59, output: 0.79 },
          stale: false,
        },
        // Bare id — must be normalised to a full `<provider>/<model>` identity.
        { id: 'llama-3.1-8b', label: 'Llama 3.1 8B' },
      ],
    },
  ],
}

describe('parseProviderCapabilities', () => {
  it('parses providers and keeps per-model metadata', () => {
    const caps = parseProviderCapabilities(CAPABILITIES_DTO)

    expect(caps).toHaveLength(1)
    expect(caps[0].id).toBe('groq')
    expect(caps[0].wire_api).toBe('openai-completions')
    expect(caps[0].auth_kind).toBe('api_key')
    expect(caps[0].model_source).toBe('bundled')
    expect(caps[0].default_model_id).toBe('groq/llama-3.3-70b')
    expect(caps[0].models?.[0]).toMatchObject({
      id: 'groq/llama-3.3-70b',
      label: 'Llama 3.3 70B',
      context_window: 131072,
      max_tokens: 8192,
      default_thinking_level: 'medium',
      supports_fast_mode: false,
      cost: { input: 0.59, output: 0.79 },
      capabilities: { tool_calling: true, vision: false, stream_with_tools: true, cache_control: false },
      stale: false,
    })
  })

  it('rejects malformed responses', () => {
    expect(() => parseProviderCapabilities(null)).toThrow(/must be an object/)
    expect(() => parseProviderCapabilities({})).toThrow(/missing 'providers'/)
    expect(() => parseProviderCapabilities({ providers: {} })).toThrow(/must be an array/)
  })

  it('drops entries without a usable provider id', () => {
    const caps = parseProviderCapabilities({ providers: [{ label: 'ghost' }, { id: '' }, { id: 'groq' }] })
    expect(caps.map((c) => c.id)).toEqual(['groq'])
  })
})

describe('modelIdentity', () => {
  it('prefixes bare model ids with the provider', () => {
    expect(modelIdentity('groq', 'llama-3.3-70b')).toBe('groq/llama-3.3-70b')
  })

  it('is idempotent for full identities', () => {
    expect(modelIdentity('groq', 'groq/llama-3.3-70b')).toBe('groq/llama-3.3-70b')
  })

  it('preserves slashes inside the model id', () => {
    expect(modelIdentity('openrouter', 'meta-llama/llama-3.3-70b')).toBe('openrouter/meta-llama/llama-3.3-70b')
  })
})

describe('capability option mapping', () => {
  const cap = parseProviderCapabilities(CAPABILITIES_DTO)[0]

  it('exposes every model as a <provider>/<model> option', () => {
    const options = modelOptionsFor(cap)

    expect(options.map((o) => o.value)).toEqual(['groq/llama-3.3-70b', 'groq/llama-3.1-8b'])
    expect(options[0].label).toBe('Groq · Llama 3.3 70B')
    expect(options[1].label).toBe('Groq · Llama 3.1 8B')
  })

  it('maps providers by name for agent config values', () => {
    expect(providerOptionsFrom([cap])).toEqual([{ value: 'groq', label: 'Groq' }])
  })
})

describe('isCatalogueStale', () => {
  it('flags a stale model_source', () => {
    expect(isCatalogueStale({ id: 'groq', model_source: 'stale' })).toBe(true)
  })

  it('flags a stale model entry', () => {
    expect(isCatalogueStale({ id: 'groq', models: [{ id: 'groq/x', stale: true }] })).toBe(true)
  })

  it('is false for a fresh catalogue and for an unknown provider', () => {
    expect(isCatalogueStale(parseProviderCapabilities(CAPABILITIES_DTO)[0])).toBe(false)
    expect(isCatalogueStale(undefined)).toBe(false)
  })
})

describe('useProviderCapabilities', () => {
  beforeEach(() => {
    get.mockReset()
  })

  it('fetches and parses the capabilities endpoint', async () => {
    get.mockResolvedValue(CAPABILITIES_DTO)

    const { result } = renderHook(() => useProviderCapabilities())
    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(get).toHaveBeenCalledWith('/v1/providers/capabilities')
    expect(result.current.error).toBeNull()
    expect(result.current.capabilities.map((c) => c.id)).toEqual(['groq'])
  })

  it('surfaces a fetch failure instead of an empty catalogue', async () => {
    get.mockRejectedValue(new Error('gateway down'))

    const { result } = renderHook(() => useProviderCapabilities())
    await waitFor(() => expect(result.current.isLoading).toBe(false))

    expect(result.current.error).toBe('gateway down')
    expect(result.current.capabilities).toEqual([])
  })
})
