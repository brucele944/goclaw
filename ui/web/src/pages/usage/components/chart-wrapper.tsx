import { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { BarChart3 } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

interface ChartWrapperProps {
  title: string;
  subtitle?: string;
  loading?: boolean;
  empty?: boolean;
  emptyText?: string;
  height?: number;
  children: ReactNode;
  className?: string;
  actions?: ReactNode;
}

export function ChartWrapper({
  title,
  subtitle,
  loading,
  empty,
  emptyText,
  height = 300,
  children,
  className,
  actions,
}: ChartWrapperProps) {
  const { t } = useTranslation("usage");
  // The chart itself is a canvas/SVG with no text alternative, so expose the
  // rendered plot as a single labelled image for assistive tech.
  const chartLabel = subtitle ? `${title}. ${subtitle}` : title;

  return (
    <div className={cn("rounded-lg border bg-card p-4", className)}>
      <div className="mb-4 flex items-start justify-between">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          {subtitle && <p className="mt-0.5 text-xs text-muted-foreground">{subtitle}</p>}
        </div>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>

      {loading ? (
        <div className="space-y-2" style={{ height }}>
          <Skeleton className="h-full w-full" />
        </div>
      ) : empty ? (
        <div
          className="flex flex-col items-center justify-center text-center"
          style={{ height }}
        >
          <div className="mb-2 rounded-full bg-muted p-2">
            <BarChart3 className="h-5 w-5 text-muted-foreground" aria-hidden="true" />
          </div>
          <p className="text-xs text-muted-foreground">{emptyText ?? t("analytics.noData")}</p>
        </div>
      ) : (
        <div role="img" aria-label={chartLabel}>
          {children}
        </div>
      )}
    </div>
  );
}
