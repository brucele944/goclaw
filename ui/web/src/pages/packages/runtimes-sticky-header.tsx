import { useTranslation } from "react-i18next";
import { RefreshCw, CheckCircle2, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { usePackageRuntimes } from "./hooks/use-package-runtimes";

/**
 * RuntimesStickyHeader — compact horizontal runtime status strip.
 * Shown above the tabs list and stays visible when switching tabs.
 */
export function RuntimesStickyHeader() {
  const { t } = useTranslation("packages");
  const { runtimes, loading, refresh } = usePackageRuntimes();

  if (!runtimes?.runtimes?.length && !loading) return null;

  return (
    <div className="flex flex-wrap items-center gap-2 py-2 px-1">
      <span className="text-xs font-medium text-muted-foreground shrink-0">
        {t("runtimes.title")}:
      </span>
      <div className="flex flex-wrap gap-1.5 flex-1 min-w-0">
        {runtimes?.runtimes?.map((rt) => (
          <span
            key={rt.name}
            className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium border ${
              rt.available
                ? "border-success/25 bg-success-subtle dark:bg-green-950/20"
                : "border-destructive/25 bg-destructive-subtle"
            }`}
          >
            {rt.available ? (
              <CheckCircle2 className="h-3 w-3" />
            ) : (
              <XCircle className="h-3 w-3" />
            )}
            {rt.name}
            {rt.version && <span className="font-mono opacity-70">{rt.version}</span>}
          </span>
        ))}
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 px-2 text-xs shrink-0"
        onClick={refresh}
        disabled={loading}
        title={t("actions.refresh", { defaultValue: "Refresh" })}
      >
        <RefreshCw className={`h-3 w-3 ${loading ? "animate-spin" : ""}`} />
      </Button>
    </div>
  );
}
