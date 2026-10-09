/**
 * Regression test for the AppLayout mobile navigation sheet.
 *
 * The sheet must stay open after it is opened, and must close on a route change.
 * Keying the route-change effect on `mobileSidebarOpen` as well as the pathname
 * re-runs it in the commit that opens the sheet and closes it immediately,
 * making the primary navigation unreachable below the tablet breakpoint.
 */
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router";
import { AppLayout } from "@/components/layout/app-layout";
import { WsContext } from "@/hooks/use-ws";
import { useUiStore } from "@/stores/use-ui-store";
import i18n from "@/i18n";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

/** Every media query reports a match so the layout renders its tablet branch. */
function stubMatchMedia() {
  window.matchMedia = ((query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

function AgentsPage() {
  const navigate = useNavigate();
  return (
    <button type="button" data-testid="go-settings" onClick={() => navigate("/settings")}>
      open settings
    </button>
  );
}

let container: HTMLDivElement;
let root: Root;
let queryClient: QueryClient;

beforeEach(async () => {
  await i18n.changeLanguage("en");
  stubMatchMedia();
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  useUiStore.setState({ mobileSidebarOpen: false });
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

const openSheet = () => useUiStore.getState().setMobileSidebarOpen(true);
// SheetContent renders through a Radix portal, so it lands in document.body.
const sheetOpen = () => document.body.querySelector('[data-slot="sheet-content"]') !== null;
const find = (selector: string) => container.querySelector<HTMLElement>(selector)!;

/** Minimal WS/HTTP context: the layout's banners and topbar only read from it. */
const wsStub = {
  ws: {
    on: () => {},
    off: () => {},
    send: async () => ({}),
    isConnected: () => false,
  } as never,
  http: { get: async () => ({ configured: false }) } as never,
};

function renderAt(path: string) {
  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <WsContext.Provider value={wsStub}>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route element={<AppLayout />}>
                <Route path="agents" element={<AgentsPage />} />
                <Route path="settings" element={<div>settings page</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </WsContext.Provider>
      </QueryClientProvider>,
    );
  });
}

describe("AppLayout mobile navigation", () => {
  it("keeps the nav sheet open after it is opened", () => {
    renderAt("/agents");
    expect(sheetOpen()).toBe(false);

    act(openSheet);
    expect(sheetOpen()).toBe(true);
  });

  it("closes the nav sheet when the route changes", () => {
    renderAt("/agents");
    act(openSheet);
    expect(sheetOpen()).toBe(true);

    act(() => find('[data-testid="go-settings"]').click());

    expect(useUiStore.getState().mobileSidebarOpen).toBe(false);
    expect(sheetOpen()).toBe(false);
  });
});
