import { AlertTriangle, Info, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  useProviderCapability,
  useProviderCatalogueRefresh,
} from "@/pages/providers/hooks/use-provider-capabilities";
import { cn } from "@/lib/utils";

interface ModelCatalogueStatusProps {
  /** True when the gateway served a cached catalogue after a failed refresh. */
  stale?: boolean;
  /** Message of a failed capability-catalogue request or refresh. */
  error?: string | null;
  /** The catalogue answered but has no entry for this provider. */
  notListed?: boolean;
  /** The provider's catalogue resolved to zero models. */
  empty?: boolean;
  refreshing?: boolean;
  onRefresh: () => void;
  className?: string;
}

/**
 * Non-blocking state line for a model picker fed by the capability catalogue.
 *
 * Every degraded catalogue state ("could not refresh", "no such provider",
 * "nothing discovered", "serving a cached copy") used to be indistinguishable
 * from an empty list, which made a discovery outage look like a tenant with no
 * models. This renders the state and a refresh affordance; it never disables the
 * picker, because a stale catalogue is still selectable and a custom model can
 * always be typed.
 */
export function ModelCatalogueStatus({
  stale,
  error,
  notListed,
  empty,
  refreshing,
  onRefresh,
  className,
}: ModelCatalogueStatusProps) {
  const { t } = useTranslation("providers");

  const state = error
    ? { tone: "error", message: t("catalogue.error", { message: error }) }
    : notListed
      ? { tone: "muted", message: t("catalogue.notListed") }
      : empty
        ? { tone: "muted", message: t("catalogue.empty") }
        : stale
          ? { tone: "warning", message: t("catalogue.stale") }
          : null;

  if (!state) return null;

  const toneClass =
    state.tone === "error"
      ? "text-destructive"
      : state.tone === "warning"
        ? "text-amber-700 dark:text-amber-400"
        : "text-muted-foreground";

  return (
    <div className={cn("flex items-center gap-1.5 text-xs", toneClass, className)}>
      {state.tone === "muted" ? (
        <Info className="h-3 w-3 shrink-0" />
      ) : (
        <AlertTriangle className="h-3 w-3 shrink-0" />
      )}
      <span className="min-w-0 flex-1 truncate" title={state.message}>
        {state.message}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="xs"
        className="shrink-0"
        disabled={refreshing}
        onClick={onRefresh}
      >
        <RefreshCw className={cn("h-3 w-3", refreshing && "animate-spin")} />
        {refreshing ? t("catalogue.refreshing") : t("catalogue.refresh")}
      </Button>
    </div>
  );
}

/**
 * The same state line, wired to a provider by name.
 *
 * For a picker that renders the model list itself: it reads the provider's
 * capability entry, reports a stale or failed catalogue, and refreshes through
 * the per-provider discovery endpoint. `providerId` is only needed to make that
 * refresh actually re-discover (the catalogue DTO itself is a cache read).
 */
export function ProviderCatalogueStatus({
  provider,
  providerId,
  className,
}: {
  provider: string;
  providerId?: string;
  className?: string;
}) {
  const { models, loading, refreshing, error, notListed, stale } = useProviderCapability(provider);
  const {
    refresh,
    refreshing: refreshRunning,
    refreshError,
  } = useProviderCatalogueRefresh();

  return (
    <ModelCatalogueStatus
      className={className}
      stale={stale || models.some((model) => model.stale)}
      error={error ?? refreshError}
      notListed={notListed}
      empty={!loading && models.length === 0}
      refreshing={refreshing || refreshRunning}
      onRefresh={() => void refresh(providerId ? [providerId] : [])}
    />
  );
}
