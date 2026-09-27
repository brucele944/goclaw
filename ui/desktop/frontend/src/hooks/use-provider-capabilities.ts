/**
 * Desktop hook for fetching LLM provider capabilities.
 * Mirrors useTtsCapabilities (src/hooks/use-tts-capabilities.ts) but for
 * GET /v1/providers/capabilities — the source of `wire_api`, `auth_kind` and
 * the per-model catalogue with `<provider>/<model>` identities.
 */
import { useState, useEffect, useCallback } from 'react'
import { getApiClient } from '../lib/api'
import { parseProviderCapabilities } from '../api/provider-capabilities'
import type { ProviderCapability } from '../types/provider'

export interface UseProviderCapabilitiesResult {
  capabilities: ProviderCapability[]
  isLoading: boolean
  error: string | null
  refetch: () => Promise<void>
}

export function useProviderCapabilities(): UseProviderCapabilitiesResult {
  const [capabilities, setCapabilities] = useState<ProviderCapability[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const refetch = useCallback(async () => {
    setIsLoading(true)
    setError(null)
    try {
      const res = await getApiClient().get<unknown>('/v1/providers/capabilities')
      setCapabilities(parseProviderCapabilities(res))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load provider capabilities')
      setCapabilities([])
    } finally {
      setIsLoading(false)
    }
  }, [])

  useEffect(() => { void refetch() }, [refetch])

  return { capabilities, isLoading, error, refetch }
}
