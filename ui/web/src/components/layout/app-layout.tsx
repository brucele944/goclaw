import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router";
import { WifiOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Sidebar } from "./sidebar";
import { Topbar } from "./topbar";
import { BackgroundErrorBanner } from "./background-error-banner";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { ErrorBoundary } from "@/components/shared/error-boundary";
import { useUiStore } from "@/stores/use-ui-store";
import { useAuthStore } from "@/stores/use-auth-store";
import { useIsTablet } from "@/hooks/use-media-query";

/**
 * Returns a stable key for pages that handle their own sub-navigation
 * (e.g. /chat/:sessionKey, /sessions/:key). Prevents ErrorBoundary from
 * remounting the entire page when only the dynamic param changes.
 */
function stableErrorBoundaryKey(pathname: string): string {
  // Strip dynamic segments: /chat/anything → /chat
  const base = pathname.replace(/^(\/[^/]+)\/.*$/, "$1");
  return base;
}

export function AppLayout() {
  const { t } = useTranslation("common");
  const location = useLocation();
  const sidebarCollapsed = useUiStore((s) => s.sidebarCollapsed);
  const mobileSidebarOpen = useUiStore((s) => s.mobileSidebarOpen);
  const setMobileSidebarOpen = useUiStore((s) => s.setMobileSidebarOpen);
  const connected = useAuthStore((s) => s.connected);
  const isMobile = useIsTablet();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const mainRef = useRef<HTMLElement>(null);
  const basePath = stableErrorBoundaryKey(location.pathname);
  const prevBasePath = useRef(basePath);
  const prevSidebarPath = useRef(location.pathname);

  // Close the mobile nav on route change (e.g. programmatic navigation, back button).
  // Keyed on the pathname alone: depending on `mobileSidebarOpen` re-runs this on open
  // and closes the sheet in the same commit that opened it.
  useEffect(() => {
    if (prevSidebarPath.current === location.pathname) return;
    prevSidebarPath.current = location.pathname;
    setMobileSidebarOpen(false);
  }, [location.pathname, setMobileSidebarOpen]);

  // Move focus to the main landmark when the base route changes so screen-reader
  // and keyboard users land on the new page. Param-only changes (/chat/:key) keep
  // their own focus, and the initial mount does not steal focus from the skip link.
  useEffect(() => {
    if (prevBasePath.current === basePath) return;
    prevBasePath.current = basePath;
    mainRef.current?.focus({ preventScroll: true });
  }, [basePath]);

  return (
    <div className="flex h-dvh overflow-hidden safe-top">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:top-4 focus:left-4 focus:z-[60] focus:rounded-md focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:shadow-lg focus:ring-2 focus:ring-ring focus:outline-none"
      >
        {t("skipToContent")}
      </a>
      {isMobile ? (
        <Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>
          <SheetContent
            side="left"
            title={t("navigation")}
            className="w-64 max-w-[85vw] gap-0 border-r-0"
          >
            <Sidebar collapsed={false} onNavItemClick={() => setMobileSidebarOpen(false)} />
          </SheetContent>
        </Sheet>
      ) : (
        <Sidebar collapsed={sidebarCollapsed} />
      )}
      <div className="flex flex-1 flex-col overflow-hidden">
        <Topbar settingsOpen={settingsOpen} onSettingsOpenChange={setSettingsOpen} />
        <BackgroundErrorBanner settingsOpen={settingsOpen} onOpenSettings={() => setSettingsOpen(true)} />
        {!connected && (
          <div
            role="status"
            aria-live="polite"
            className="flex items-center gap-2 border-b border-destructive/30 bg-destructive/10 px-4 py-2 text-sm text-destructive"
          >
            <WifiOff className="h-4 w-4 shrink-0" aria-hidden="true" />
            <span>{t("disconnectedGateway")}</span>
          </div>
        )}
        <main
          id="main-content"
          ref={mainRef}
          tabIndex={-1}
          className="min-w-0 flex-1 overflow-y-auto outline-none"
        >
          <ErrorBoundary key={stableErrorBoundaryKey(location.pathname)}>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>
    </div>
  );
}
