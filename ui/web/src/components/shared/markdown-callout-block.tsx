/**
 * CalloutBlock — styled callout/admonition block rendered from [!type] blockquotes.
 * Supports: note, info, warning, tip, danger, important
 * Full dark mode support via Tailwind dark: variants.
 */
import { Info, AlertTriangle, Lightbulb, AlertOctagon, Star } from "lucide-react";
import type { CalloutType } from "@/lib/remark-callouts";

interface CalloutConfig {
  icon: React.ElementType;
  borderColor: string;
  bgColor: string;
  titleColor: string;
  iconColor: string;
}

const CALLOUT_CONFIG: Record<CalloutType, CalloutConfig> = {
  note: {
    icon: Info,
    borderColor: "border-info/25",
    bgColor: "bg-info-subtle",
    titleColor: "text-info-text",
    iconColor: "text-info-text",
  },
  info: {
    icon: Info,
    borderColor: "border-info/25",
    bgColor: "bg-info-subtle",
    titleColor: "text-info-text",
    iconColor: "text-info-text",
  },
  warning: {
    icon: AlertTriangle,
    borderColor: "border-warning/25",
    bgColor: "bg-warning-subtle",
    titleColor: "text-warning-text",
    iconColor: "text-warning-text",
  },
  tip: {
    icon: Lightbulb,
    borderColor: "border-success/25",
    bgColor: "bg-success-subtle",
    titleColor: "text-success-text",
    iconColor: "text-success-text",
  },
  danger: {
    icon: AlertOctagon,
    borderColor: "border-destructive/25",
    bgColor: "bg-destructive-subtle",
    titleColor: "text-destructive-text",
    iconColor: "text-destructive-text",
  },
  important: {
    icon: Star,
    borderColor: "border-purple-400 dark:border-purple-500",
    bgColor: "bg-purple-50 dark:bg-purple-950/40",
    titleColor: "text-purple-700 dark:text-purple-300",
    iconColor: "text-purple-500 dark:text-purple-400",
  },
};

interface CalloutBlockProps {
  calloutType?: string;
  calloutTitle?: string;
  children?: React.ReactNode;
}

export function CalloutBlock({ calloutType, calloutTitle, children }: CalloutBlockProps) {
  const type = (calloutType as CalloutType) ?? "note";
  const config = CALLOUT_CONFIG[type] ?? CALLOUT_CONFIG.note;
  const Icon = config.icon;

  return (
    <div
      className={`not-prose my-4 rounded-r-md border-l-4 px-4 py-3 ${config.borderColor} ${config.bgColor}`}
    >
      <div className={`mb-1 flex items-center gap-1.5 text-sm font-semibold ${config.titleColor}`}>
        <Icon className={`h-4 w-4 shrink-0 ${config.iconColor}`} />
        <span>{calloutTitle}</span>
      </div>
      <div className="prose prose-sm dark:prose-invert max-w-none text-sm">
        {children}
      </div>
    </div>
  );
}
