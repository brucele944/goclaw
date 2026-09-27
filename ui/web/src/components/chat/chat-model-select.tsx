import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Loader2 } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useProviderCapabilities, useProviderCatalogueRefresh } from "@/pages/providers/hooks/use-provider-capabilities";
import { isProviderCatalogueStale } from "@/types/provider";

/** Sentinel for "no override — use the agent's own model". */
const AGENT_DEFAULT = "__agent_default__";

interface ChatModelSelectProps {
  /** Selected model identity, "<provider>/<model>". Empty = the agent's model. */
  value: string;
  onChange: (identity: string) => void;
  disabled?: boolean;
}

/**
 * Per-request model override for the chat header.
 *
 * The list is the tenant's capability catalogue grouped by provider, and the
 * value it emits is the DTO identity "<provider>/<model>" — exactly what
 * `chat.send` accepts in its `model` field. Clearing the selection ("Agent
 * default") emits "" so the caller sends no override and the agent decides.
 *
 * A catalogue that failed, is stale, or is empty is never silent: the warning
 * icon carries the reason and refreshes the catalogue when clicked, and the
 * picker stays usable (the agent default is always selectable).
 */
export function ChatModelSelect({ value, onChange, disabled }: ChatModelSelectProps) {
  const { t } = useTranslation("chat");
  const { providers, loading, refreshing, error } = useProviderCapabilities();
  const {
    refresh,
    refreshing: catalogueRefreshRunning,
    refreshError,
  } = useProviderCatalogueRefresh();

  const entries = useMemo(
    () => providers.flatMap((provider) => provider.models.map((model) => ({ provider, model }))),
    [providers],
  );
  const selected = entries.find((e) => e.model.id === value) ?? null;
  const catalogueStale = providers.some(
    (p) => isProviderCatalogueStale(p) || p.models.some((m) => m.stale),
  );
  // Only the providers whose catalogue is untrustworthy are re-discovered: an
  // empty list is as suspect as a stale one, a healthy provider is left alone.
  const degradedProviderIds = providers
    .filter((p) => isProviderCatalogueStale(p) || p.models.length === 0)
    .map((p) => p.provider_id)
    .filter(Boolean);

  const unavailable = error ?? refreshError;
  const warning = unavailable
    ? t("modelSelector.unavailable", { message: unavailable })
    : !loading && entries.length === 0
      ? t("modelSelector.empty")
      : catalogueStale
        ? t("modelSelector.stale")
        : null;

  return (
    <div className="flex items-center gap-1">
      <Select
        value={value || AGENT_DEFAULT}
        onValueChange={(next) => onChange(next === AGENT_DEFAULT ? "" : next)}
        disabled={disabled}
      >
        <SelectTrigger
          size="sm"
          className="h-7 max-w-[13rem] text-xs"
          title={selected ? selected.model.id : t("modelSelector.agentDefault")}
          aria-label={t("modelSelector.label")}
        >
          <SelectValue placeholder={t("modelSelector.agentDefault")} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={AGENT_DEFAULT}>{t("modelSelector.agentDefault")}</SelectItem>
          {providers
            .filter((provider) => provider.models.length > 0)
            .map((provider) => (
              <SelectGroup key={provider.id}>
                <SelectLabel>{provider.label}</SelectLabel>
                {provider.models.map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    {model.label}
                    <span className="font-mono text-2xs text-muted-foreground">{model.id}</span>
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
        </SelectContent>
      </Select>
      {refreshing || catalogueRefreshRunning ? (
        <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-hidden="true" />
      ) : warning ? (
        <button
          type="button"
          onClick={() => void refresh(degradedProviderIds)}
          title={warning}
          aria-label={warning}
          className="text-amber-700 hover:text-amber-600 dark:text-amber-400"
        >
          <AlertTriangle className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  );
}
