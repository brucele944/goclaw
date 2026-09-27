import { AlertTriangle, CircleSlash2, Key, Link2, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { PROVIDER_TYPES } from "@/constants/providers";
import type { EffectiveChatGPTOAuthRoutingStrategy } from "@/types/agent";
import { getChatGPTOAuthProviderRouting } from "@/types/provider";
import type { ChatGPTOAuthAvailability } from "./hooks/use-chatgpt-oauth-provider-statuses";
import type { ProviderData } from "./hooks/use-providers";

type BadgeVariant = "default" | "secondary" | "outline";

const SPECIAL_VARIANTS: Record<string, BadgeVariant> = {
  anthropic_native: "default",
  chatgpt_oauth: "default",
  claude_cli: "outline",
  acp: "outline",
};

/** Derive badge labels from PROVIDER_TYPES constant (single source of truth). */
export const PROVIDER_TYPE_BADGE: Record<string, { label: string; variant: BadgeVariant }> = Object.fromEntries(
  PROVIDER_TYPES.map((pt) => [
    pt.value,
    { label: pt.label.replace(/ \(.*\)$/, ""), variant: SPECIAL_VARIANTS[pt.value] ?? "secondary" },
  ]),
);

/**
 * i18n keys for the transport families of `llm_providers.wire_api`.
 *
 * A value missing here is not an error: it is a transport this build does not
 * know yet and the raw value is rendered instead, so a gateway that adds one
 * stays readable without a UI release.
 */
const WIRE_API_LABEL_KEY: Record<string, string> = {
  "openai-completions": "wireApi.openaiCompletions",
  "anthropic-messages": "wireApi.anthropicMessages",
  "cli-delegated": "wireApi.cliDelegated",
};

/** i18n keys for `llm_providers.auth_kind`; unknown values fall back to the raw value. */
const AUTH_KIND_LABEL_KEY: Record<string, string> = {
  api_key: "authKind.apiKey",
  oauth_browser: "authKind.oauthBrowser",
  service_account: "authKind.serviceAccount",
  cli_delegated: "authKind.cliDelegated",
  none: "authKind.none",
};

/**
 * The dispatch identity of a provider, shown next to its (legacy) provider_type:
 * which wire protocol it speaks and which credential ladder it uses. Renders
 * nothing when the gateway predates those columns.
 */
export function ProviderWireMeta({
  provider,
  className,
}: {
  provider: Pick<ProviderData, "wire_api" | "auth_kind">;
  className?: string;
}) {
  const { t } = useTranslation("providers");
  const wireApi = provider.wire_api?.trim() ?? "";
  const authKind = provider.auth_kind?.trim() ?? "";
  if (!wireApi && !authKind) return null;
  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {wireApi && (
        <Badge
          variant="secondary"
          className="text-2xs font-mono"
          title={`${t("detail.wireApi")}: ${wireApi}`}
        >
          {WIRE_API_LABEL_KEY[wireApi] ? t(WIRE_API_LABEL_KEY[wireApi]) : wireApi}
        </Badge>
      )}
      {authKind && (
        <Badge
          variant="outline"
          className="text-2xs"
          title={`${t("detail.authKind")}: ${authKind}`}
        >
          {AUTH_KIND_LABEL_KEY[authKind] ? t(AUTH_KIND_LABEL_KEY[authKind]) : authKind}
        </Badge>
      )}
    </div>
  );
}

export interface ChatGPTOAuthPoolOwnership {
  membersByOwner: Map<string, string[]>;
  ownerByMember: Map<string, string>;
  strategyByOwner: Map<string, EffectiveChatGPTOAuthRoutingStrategy>;
}

export function getChatGPTOAuthPoolOwnership(
  providers: ProviderData[],
  options?: { enabledOnly?: boolean },
): ChatGPTOAuthPoolOwnership {
  const membersByOwner = new Map<string, string[]>();
  const ownerByMember = new Map<string, string>();
  const strategyByOwner = new Map<string, EffectiveChatGPTOAuthRoutingStrategy>();
  const enabledOnly = options?.enabledOnly ?? false;
  const eligibleProviders = enabledOnly
    ? providers.filter((provider) => provider.provider_type === "chatgpt_oauth" && provider.enabled)
    : providers.filter((provider) => provider.provider_type === "chatgpt_oauth");
  const eligibleProvidersByName = new Map(
    eligibleProviders.map((provider) => [provider.name, provider]),
  );

  for (const provider of providers) {
    if (provider.provider_type !== "chatgpt_oauth") continue;
    if (enabledOnly && !provider.enabled) continue;
    const routing = getChatGPTOAuthProviderRouting(provider.settings);
    if (!routing) continue;
    strategyByOwner.set(provider.name, routing.strategy);
    const eligibleMembers = routing.extraProviderNames.filter((memberName) => eligibleProvidersByName.has(memberName));
    if (eligibleMembers.length === 0) continue;
    membersByOwner.set(provider.name, eligibleMembers);
    for (const memberName of eligibleMembers) {
      if (!ownerByMember.has(memberName)) {
        ownerByMember.set(memberName, provider.name);
      }
    }
  }

  return {
    membersByOwner,
    ownerByMember,
    strategyByOwner,
  };
}

function providerHierarchyOrder(
  provider: ProviderData,
  indexByName: Map<string, number>,
  ownership: ChatGPTOAuthPoolOwnership,
): [number, number] {
  const index = indexByName.get(provider.name) ?? 0;
  if (provider.provider_type !== "chatgpt_oauth") {
    return [index * 3, index];
  }

  const ownerName = ownership.ownerByMember.get(provider.name);
  if (ownerName) {
    const ownerIndex = indexByName.get(ownerName) ?? index;
    return [ownerIndex * 3 + 1, index];
  }

  if (ownership.membersByOwner.has(provider.name)) {
    return [index * 3, index];
  }

  return [index * 3 + 2, index];
}

export function sortProvidersForPoolHierarchy(
  providers: ProviderData[],
  ownership: ChatGPTOAuthPoolOwnership,
): ProviderData[] {
  const indexByName = new Map(providers.map((provider, index) => [provider.name, index]));
  return [...providers].sort((left, right) => {
    const [leftGroup, leftIndex] = providerHierarchyOrder(left, indexByName, ownership);
    const [rightGroup, rightIndex] = providerHierarchyOrder(right, indexByName, ownership);
    if (leftGroup !== rightGroup) return leftGroup - rightGroup;
    return leftIndex - rightIndex;
  });
}

/** Shared API key status indicator. */
export function ProviderApiKeyBadge({
  provider,
  oauthAvailability,
}: {
  provider: ProviderData;
  oauthAvailability?: ChatGPTOAuthAvailability;
}) {
  const { t } = useTranslation("providers");
  if (provider.provider_type === "chatgpt_oauth") {
    if (oauthAvailability === "needs_sign_in") {
      return (
        <span className="flex items-center gap-1 text-xs-plus text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-3 w-3" />{t("card.signInNeeded")}
        </span>
      );
    }
    if (oauthAvailability === "disabled") {
      return (
        <span className="flex items-center gap-1 text-xs-plus text-muted-foreground">
          <CircleSlash2 className="h-3 w-3" />{t("card.disabled")}
        </span>
      );
    }
    return (
      <span className="flex items-center gap-1 text-xs-plus text-emerald-600 dark:text-emerald-400">
        <Link2 className="h-3 w-3" />{t("card.connected")}
      </span>
    );
  }
  if (provider.provider_type === "claude_cli") {
    return (
      <span className="flex items-center gap-1 text-xs-plus text-emerald-600 dark:text-emerald-400">
        <ShieldCheck className="h-3 w-3" />{t("card.authenticated")}
      </span>
    );
  }
  if (provider.api_key === "***") {
    return (
      <span className="flex items-center gap-1 text-xs-plus text-muted-foreground">
        <Key className="h-3 w-3" />{t("card.apiKeySet")}
      </span>
    );
  }
  return (
    <span className="text-xs-plus text-muted-foreground/60">{t("apiKey.notSet")}</span>
  );
}
