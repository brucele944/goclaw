import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { Download, Loader2, AlertTriangle, CheckCircle2, XCircle } from "lucide-react";
import { ROUTES } from "@/lib/constants";
import type { RuntimeStatus } from "./hooks/use-runtimes";

interface MissingDepsPanelProps {
  missing: string[];
  onInstallItem: (dep: string) => Promise<unknown>;
  runtimes?: RuntimeStatus | null;
}

type ItemStatus = "idle" | "installing" | "success" | "error";

export function MissingDepsPanel({ missing, onInstallItem, runtimes }: MissingDepsPanelProps) {
  const { t } = useTranslation("skills");
  const [itemStatus, setItemStatus] = useState<Record<string, ItemStatus>>({});

  const runtimesReady = runtimes?.ready ?? true;
  const missingRuntimes = runtimes?.runtimes?.filter((r) => !r.available) ?? [];

  const system = missing.filter((d) => !d.includes(":"));
  const pip = missing.filter((d) => d.startsWith("pip:")).map((d) => d.slice(4));
  const npm = missing.filter((d) => d.startsWith("npm:")).map((d) => d.slice(4));

  if (missing.length === 0 && runtimesReady) return null;

  async function handleInstall(dep: string) {
    setItemStatus((s) => ({ ...s, [dep]: "installing" }));
    try {
      await onInstallItem(dep);
      setItemStatus((s) => ({ ...s, [dep]: "success" }));
    } catch {
      setItemStatus((s) => ({ ...s, [dep]: "error" }));
    }
  }

  function renderDepRow(dep: string, label: string) {
    const status = itemStatus[dep] ?? "idle";
    return (
      <div key={dep} className="flex items-center justify-between gap-2 py-1 px-2 -mx-2 rounded hover:bg-warning-subtle transition-colors">
        <span className="text-xs text-warning-text font-mono">{label}</span>
        <div className="flex items-center gap-1.5 shrink-0">
          {status === "success" && <CheckCircle2 className="h-3.5 w-3.5 text-success-text" />}
          {status === "error" && <XCircle className="h-3.5 w-3.5 text-destructive-text" />}
          {status !== "success" && (
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2 text-xs border hover:bg-warning-subtle dark:border-amber-700 dark:text-amber-200"
              onClick={() => handleInstall(dep)}
              disabled={status === "installing" || !runtimesReady}
              title={!runtimesReady ? t("deps.runtimeRequired") : undefined}
            >
              {status === "installing" ? (
                <Loader2 className="h-3 w-3 animate-spin" />
              ) : (
                <>
                  <Download className="mr-1 h-3 w-3" />
                  {t("deps.installItem")}
                </>
              )}
            </Button>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3 mb-4">
      {/* Runtime prerequisites warning */}
      {!runtimesReady && (
        <div className="rounded-lg border border-destructive/25 bg-destructive-subtle p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="h-4 w-4 text-destructive-text mt-0.5 shrink-0" />
            <div className="space-y-1">
              <h3 className="text-sm font-medium dark:text-red-200">
                {t("deps.runtimeMissing")}
              </h3>
              <p className="text-xs text-destructive-text">
                {t("deps.runtimeMissingDesc")}
              </p>
              <Link
                to={ROUTES.PACKAGES}
                className="inline-flex text-xs font-medium text-destructive-text underline underline-offset-4 hover:text-red-900 dark:hover:text-red-100"
              >
                {t("deps.runtimeMissingAction")}
              </Link>
              <div className="flex flex-wrap gap-1.5 mt-2">
                {missingRuntimes.map((r) => (
                  <span
                    key={r.name}
                    className="inline-flex items-center rounded-md bg-destructive-subtle px-2 py-0.5 text-xs font-medium text-destructive-text"
                  >
                    {r.name}
                  </span>
                ))}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Missing package dependencies — only show when runtimes are ready (installing deps without runtime is pointless) */}
      {missing.length > 0 && runtimesReady && (
        <div className="rounded-lg border border-warning/25 bg-warning-subtle p-4">
          <h3 className="text-sm font-medium dark:text-amber-200 mb-2">
            {t("deps.missingTitle")}
          </h3>
          <div className="space-y-1">
            {system.length > 0 && (
              <div>
                <p className="text-xs font-medium text-warning-text mb-0.5">
                  {t("deps.systemLabel")}
                </p>
                {system.map((pkg) => renderDepRow(pkg, pkg))}
              </div>
            )}
            {pip.length > 0 && (
              <div>
                <p className="text-xs font-medium text-warning-text mb-0.5">
                  {t("deps.pythonLabel")}
                </p>
                {pip.map((pkg) => renderDepRow(`pip:${pkg}`, pkg))}
              </div>
            )}
            {npm.length > 0 && (
              <div>
                <p className="text-xs font-medium text-warning-text mb-0.5">
                  {t("deps.nodeLabel")}
                </p>
                {npm.map((pkg) => renderDepRow(`npm:${pkg}`, pkg))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
