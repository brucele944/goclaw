import { useCallback, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import { parseProvidersCapabilitiesResponse } from "@/schemas/provider.schema";
import type { ProviderCapability, ProviderCapabilityModel, ProvidersCapabilitiesResponse } from "@/types/provider";

export type { ProviderCapability, ProviderCapabilityModel };

/**
 * The tenant's provider capability catalogue (GET /v1/providers/capabilities).
 *
 * Every model picker reads model identity from here, so the endpoint is fetched
 * once and shared by react-query. `error` is surfaced instead of being folded
 * into an empty list: a failed or malformed catalogue must render as a retry
 * affordance, never as "no models".
 */
export function useProviderCapabilities(enabled = true) {
  const http = useHttp();

  const { data, isLoading, isError, error, isFetching } = useQuery({
    queryKey: queryKeys.providers.capabilities,
    enabled,
    queryFn: async (): Promise<ProvidersCapabilitiesResponse> => {
      const res = await http.get<unknown>("/v1/providers/capabilities");
      return parseProvidersCapabilitiesResponse(res);
    },
    staleTime: 60_000,
  });

  return {
    providers: data?.providers ?? [],
    loading: isLoading,
    refreshing: isFetching,
    error: isError ? (error instanceof Error ? error.message : String(error ?? "")) : null,
  };
}

/**
 * One provider's catalogue entry, by provider name slug.
 *
 * A picker fed by this can tell apart the states that all used to look like "no
 * models": the request failed (`error`), the catalogue does not list this
 * provider (`notListed`), the gateway served a cached copy that may not match
 * the upstream (`stale`), and a genuinely empty catalogue. None of them block
 * selection — a custom model can still be typed — they only have to be visible.
 */
export function useProviderCapability(providerName: string, enabled = true) {
  const { providers, loading, refreshing, error } = useProviderCapabilities(enabled);
  const name = providerName.trim();

  return useMemo(() => {
    const capability = providers.find((p) => p.id === name) ?? null;
    return {
      capability,
      models: capability?.models ?? [],
      loading,
      refreshing,
      /** Set when the catalogue request itself failed. */
      error,
      /** Set when the catalogue answered but has no entry for this provider. */
      notListed: !error && name !== "" && capability === null,
      /** True when the gateway served a cached catalogue that may not match the upstream. */
      stale: capability?.stale === true,
    };
  }, [name, providers, loading, refreshing, error]);
}

/**
 * Re-run model discovery for the given providers, then reload the catalogue.
 *
 * The capability DTO is a cache read, so it cannot clear a stale catalogue by
 * itself: `GET /v1/providers/{id}/models?refresh=true` is the only path that
 * re-runs discovery *and* classifies the failure, which is why a retry goes
 * through it and its `error`/`error_class` are surfaced to the operator instead
 * of being swallowed. Discovery runs per provider, sequentially — an upstream
 * outage is not made worse by a fan-out.
 */
export function useProviderCatalogueRefresh() {
  const http = useHttp();
  const queryClient = useQueryClient();
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const refresh = useCallback(
    async (providerIds: readonly string[] = []) => {
      setRefreshing(true);
      setRefreshError(null);
      const failures: string[] = [];
      for (const providerId of providerIds) {
        if (!providerId) continue;
        try {
          const res = await http.get<{ error?: string; error_class?: string }>(
            `/v1/providers/${providerId}/models?refresh=true`,
          );
          if (res?.error) failures.push(res.error);
        } catch (err) {
          failures.push(err instanceof Error ? err.message : String(err));
        }
      }
      setRefreshError(failures.length > 0 ? failures.join("; ") : null);
      await queryClient.invalidateQueries({ queryKey: queryKeys.providers.capabilities });
      setRefreshing(false);
    },
    [http, queryClient],
  );

  return { refresh, refreshing, refreshError };
}
