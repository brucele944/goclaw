// Provider capability DTO — GET /v1/providers/capabilities (Viewer role, tenant-scoped).
// Mirrors the parser+hook split used by ui/desktop/frontend/src/api/tts-capabilities.ts.

import type { ProviderCapability } from '../types/provider'

export interface ProviderModelOption {
  value: string
  label: string
}

/**
 * Model identity is always `<provider>/<model>`. The gateway already serves full
 * identities, so this only prefixes bare ids (and is idempotent for full ones).
 * Model ids may themselves contain slashes (e.g. `meta-llama/llama-3.3-70b`),
 * hence the prefix test rather than a split on the first slash.
 */
export function modelIdentity(provider: string, modelId: string): string {
  const id = modelId.trim()
  if (!provider) return id
  if (id === provider || id.startsWith(`${provider}/`)) return id
  return `${provider}/${id}`
}

/** A catalogue is stale when the provider says so or any model entry is stale. */
export function isCatalogueStale(cap: ProviderCapability | undefined | null): boolean {
  if (!cap) return false
  if (cap.model_source === 'stale') return true
  return (cap.models ?? []).some((m) => m.stale === true)
}

/** Combobox options with `<provider>/<model>` values and readable labels. */
export function modelOptionsFor(cap: ProviderCapability | undefined | null): ProviderModelOption[] {
  if (!cap) return []
  const providerLabel = cap.label || cap.id
  return (cap.models ?? []).map((m) => {
    const value = modelIdentity(cap.id, m.id)
    return { value, label: m.label ? `${providerLabel} · ${m.label}` : value }
  })
}

/** Provider combobox options — value is the provider name stored in agent configs. */
export function providerOptionsFrom(caps: ProviderCapability[]): ProviderModelOption[] {
  return caps.map((c) => ({ value: c.id, label: c.label || c.id }))
}

/**
 * Parses and validates a raw GET /v1/providers/capabilities JSON response.
 * Throws if the shape is invalid; entries without a usable id are dropped.
 */
export function parseProviderCapabilities(raw: unknown): ProviderCapability[] {
  if (raw === null || raw === undefined || typeof raw !== 'object') {
    throw new Error('provider capabilities response must be an object')
  }
  const obj = raw as Record<string, unknown>
  if (!('providers' in obj)) {
    throw new Error("provider capabilities response missing 'providers' field")
  }
  if (!Array.isArray(obj.providers)) {
    throw new Error("provider capabilities response 'providers' must be an array")
  }
  const list = obj.providers as unknown[]
  return list.filter((p): p is ProviderCapability => {
    if (!p || typeof p !== 'object') return false
    const id = (p as Record<string, unknown>).id
    return typeof id === 'string' && id.length > 0
  })
}
