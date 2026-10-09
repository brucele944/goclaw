import { CheckCircle, XCircle, AlertTriangle, Info, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useToastStore, type Toast } from "@/stores/use-toast-store";
import { cn } from "@/lib/utils";

const icons: Record<Toast["variant"], typeof Info> = {
  success: CheckCircle,
  destructive: XCircle,
  warning: AlertTriangle,
  default: Info,
};

const styles: Record<Toast["variant"], string> = {
  // Tinted token surfaces. Each text colour is chosen per theme so it clears
  // 4.5:1 on the card surface (--success/--destructive are dark in light mode;
  // --success-foreground/--warning are the readable values in dark mode).
  success: "border-success/30 bg-success/10 text-success dark:text-success-foreground",
  destructive: "border-destructive/30 bg-destructive/10 text-destructive",
  warning: "border-warning/30 bg-warning/10 text-warning-foreground dark:text-warning",
  default: "border-border bg-card text-foreground",
};

/** Failures interrupt; informational toasts wait for a polite announcement. */
const roleByVariant: Record<Toast["variant"], "alert" | "status"> = {
  destructive: "alert",
  warning: "alert",
  success: "status",
  default: "status",
};

function ToastItem({ toast }: { toast: Toast }) {
  const dismiss = useToastStore((s) => s.dismiss);
  const pause = useToastStore((s) => s.pause);
  const resume = useToastStore((s) => s.resume);
  const { t } = useTranslation();
  const Icon = icons[toast.variant];

  return (
    <div
      className={cn(
        "pointer-events-auto flex w-full items-start gap-3 overflow-hidden rounded-lg border p-4 shadow-lg backdrop-blur-sm animate-in slide-in-from-bottom-2 fade-in duration-200",
        styles[toast.variant],
      )}
      role={roleByVariant[toast.variant]}
      onMouseEnter={() => pause(toast.id)}
      onMouseLeave={() => resume(toast.id)}
      onFocus={() => pause(toast.id)}
      onBlur={() => resume(toast.id)}
    >
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="flex-1 min-w-0 space-y-0.5">
        <p className="text-sm font-medium break-words">{toast.title}</p>
        {toast.message && (
          <p className="text-xs opacity-80 break-words">{toast.message}</p>
        )}
      </div>
      <button
        type="button"
        onClick={() => dismiss(toast.id)}
        aria-label={t("dismiss")}
        className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-sm opacity-50 transition-opacity hover:opacity-100"
      >
        <X className="h-3.5 w-3.5" aria-hidden="true" />
      </button>
    </div>
  );
}

export function Toaster() {
  const toasts = useToastStore((s) => s.toasts);
  const { t } = useTranslation();

  // The region stays mounted even with no toasts: screen readers only announce changes
  // inside a live region that is already registered, so a region created in the same
  // commit as its first message is usually ignored.
  return (
    <div
      role="region"
      aria-label={t("notifications")}
      aria-live="polite"
      className="fixed bottom-4 right-4 z-[100] flex max-w-sm flex-col-reverse gap-2 pointer-events-none sm:bottom-6 sm:right-6 safe-bottom safe-right"
    >
      {toasts.slice(-3).map((toast) => (
        <ToastItem key={toast.id} toast={toast} />
      ))}
    </div>
  );
}
