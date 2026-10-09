import type { CodexPoolRecentRequest } from "./hooks/use-codex-pool-activity";

export interface RequestAccentClasses {
  card: string;
  glow: string;
  stripe: string;
  marker: string;
  index: string;
  directBadge: string;
  pill: string;
  trace: string;
}

export const REQUEST_ACCENTS: RequestAccentClasses[] = [
  {
    card: "border-success/25",
    glow:
      "from-emerald-500/[0.12] via-emerald-500/[0.04] to-transparent dark:from-emerald-500/[0.16] dark:via-emerald-500/[0.06]",
    stripe: "from-emerald-400/90 via-teal-400/75 to-cyan-400/35",
    marker: "bg-success",
    index:
      "border-success/25 bg-success-subtle text-success-text dark:bg-success-subtle dark:text-emerald-200",
    directBadge:
      "border-success/25 bg-success-subtle text-success-text dark:bg-success-subtle dark:text-emerald-200",
    pill:
      "border-success/25 bg-emerald-500/[0.07] text-emerald-950 dark:bg-emerald-500/[0.12] dark:text-emerald-100",
    trace:
      "text-success-text hover:border-success/25 hover:bg-success-subtle hover:text-emerald-800 dark:text-emerald-200 dark:hover:border-success/25 dark:hover:bg-success-subtle dark:hover:text-emerald-100",
  },
  {
    card: "border-info/25",
    glow:
      "from-sky-500/[0.12] via-sky-500/[0.04] to-transparent dark:from-sky-500/[0.16] dark:via-sky-500/[0.06]",
    stripe: "from-sky-400/90 via-cyan-400/75 to-blue-400/35",
    marker: "bg-sky-500",
    index:
      "border-info/25 bg-info-subtle text-info-text dark:bg-info-subtle dark:text-sky-200",
    directBadge:
      "border-info/25 bg-info-subtle text-info-text dark:bg-info-subtle dark:text-sky-200",
    pill:
      "border-info/25 bg-sky-500/[0.07] text-sky-950 dark:bg-sky-500/[0.12] dark:text-sky-100",
    trace:
      "text-info-text hover:border-info/25 hover:bg-info-subtle hover:text-sky-800 dark:text-sky-200 dark:hover:border-info/25 dark:hover:bg-info-subtle dark:hover:text-sky-100",
  },
  {
    card: "border-warning/25",
    glow:
      "from-amber-500/[0.12] via-amber-500/[0.04] to-transparent dark:from-amber-500/[0.16] dark:via-amber-500/[0.06]",
    stripe: "from-amber-400/90 via-orange-400/75 to-yellow-400/35",
    marker: "bg-warning",
    index:
      "border-warning/25 bg-warning-subtle dark:bg-warning-subtle dark:text-amber-100",
    directBadge:
      "border-warning/25 bg-warning-subtle dark:bg-warning-subtle dark:text-amber-100",
    pill:
      "border-warning/25 bg-amber-500/[0.07] text-amber-950 dark:bg-amber-500/[0.12] dark:text-amber-50",
    trace:
      "hover:border-warning/25 hover:bg-warning-subtle hover:text-amber-900 dark:text-amber-100 dark:hover:border-warning/25 dark:hover:bg-warning-subtle dark:hover:text-amber-50",
  },
  {
    card: "border-destructive/25",
    glow:
      "from-rose-500/[0.12] via-rose-500/[0.04] to-transparent dark:from-rose-500/[0.16] dark:via-rose-500/[0.06]",
    stripe: "from-rose-400/90 via-pink-400/75 to-orange-300/35",
    marker: "bg-rose-500",
    index:
      "border-destructive/25 bg-destructive-subtle text-destructive-text dark:bg-destructive-subtle dark:text-rose-200",
    directBadge:
      "border-destructive/25 bg-destructive-subtle text-destructive-text dark:bg-destructive-subtle dark:text-rose-200",
    pill:
      "border-destructive/25 bg-rose-500/[0.07] text-rose-950 dark:bg-rose-500/[0.12] dark:text-rose-100",
    trace:
      "text-destructive-text hover:border-destructive/25 hover:bg-destructive-subtle hover:text-rose-800 dark:text-rose-200 dark:hover:border-destructive/25 dark:hover:bg-destructive-subtle dark:hover:text-rose-100",
  },
];

export function requestProviderSummary(request: CodexPoolRecentRequest): string {
  const selected = request.selected_provider?.trim();
  const served = request.provider_name?.trim();
  if (request.used_failover && selected && served && selected !== served) {
    return `${selected} -> ${served}`;
  }
  return served || selected || "";
}

export function requestAccentSeed(request: CodexPoolRecentRequest): string {
  return (
    request.provider_name?.trim() ||
    request.selected_provider?.trim() ||
    requestProviderSummary(request) ||
    request.model ||
    request.span_id
  );
}

export function requestAccentClasses(seed: string): RequestAccentClasses {
  let hash = 0;
  for (const char of seed) {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  }
  return REQUEST_ACCENTS[hash % REQUEST_ACCENTS.length] ?? REQUEST_ACCENTS[0]!;
}
