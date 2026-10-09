/**
 * Tests for the Toaster: live-region container, role per variant, dismiss label,
 * error duration and hover pause.
 *
 * NOTE: @testing-library/react is not installed in this project, so components are
 * rendered with react-dom/client + act (jsdom), following the existing convention.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ReactElement } from "react";
import { Toaster } from "@/components/ui/toaster";
import { toast, useToastStore, type Toast } from "@/stores/use-toast-store";
import i18n from "@/i18n";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const TIMER_FAKES = ["setTimeout", "clearTimeout", "Date"] as const;

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await i18n.changeLanguage("en");
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  const { toasts, dismiss } = useToastStore.getState();
  for (const item of toasts) dismiss(item.id);
  vi.useRealTimers();
});

function render(ui: ReactElement) {
  act(() => {
    root.render(ui);
  });
}

function clearToasts() {
  act(() => {
    const { toasts, dismiss } = useToastStore.getState();
    for (const item of toasts) dismiss(item.id);
  });
}

describe("Toaster", () => {
  it("exposes the container as a labelled polite live region", () => {
    render(<Toaster />);
    act(() => {
      toast.info("hello");
    });

    const region = container.querySelector('[role="region"]')!;
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.getAttribute("aria-label")).toBe("Notifications");
  });

  it("mounts the live region before any toast exists", () => {
    // Screen readers only announce changes inside an already-registered live
    // region, so the container must not be created together with its first toast.
    render(<Toaster />);
    expect(container.querySelector('[role="region"]')).not.toBeNull();
    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("labels the dismiss button", () => {
    render(<Toaster />);
    act(() => {
      toast.info("hello");
    });
    expect(container.querySelector('button[aria-label="Dismiss"]')).not.toBeNull();
  });

  it("uses role=alert only for destructive and warning variants", () => {
    render(<Toaster />);
    const cases: Array<{ variant: Toast["variant"]; role: "alert" | "status" }> = [
      { variant: "destructive", role: "alert" },
      { variant: "warning", role: "alert" },
      { variant: "success", role: "status" },
      { variant: "default", role: "status" },
    ];

    for (const { variant, role } of cases) {
      clearToasts();
      act(() => {
        useToastStore.getState().addToast({ title: variant, variant });
      });
      expect(container.querySelector(`[role="${role}"]`)).not.toBeNull();
      expect(container.querySelector(`[role="${role === "alert" ? "status" : "alert"}"]`)).toBeNull();
    }
  });

  it("keeps destructive toasts for 8 s and default toasts for 4 s", () => {
    vi.useFakeTimers({ toFake: [...TIMER_FAKES] });
    render(<Toaster />);

    act(() => {
      toast.error("boom");
    });
    act(() => {
      vi.advanceTimersByTime(4000);
    });
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    act(() => {
      vi.advanceTimersByTime(3999);
    });
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(container.querySelector('[role="alert"]')).toBeNull();

    act(() => {
      toast.info("hi");
    });
    act(() => {
      vi.advanceTimersByTime(4000);
    });
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("pauses the countdown on hover and resumes with the remaining time", () => {
    vi.useFakeTimers({ toFake: [...TIMER_FAKES] });
    render(<Toaster />);
    act(() => {
      toast.info("hi");
    });
    act(() => {
      vi.advanceTimersByTime(2000);
    });

    const item = container.querySelector('[role="status"]')!;
    act(() => {
      item.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    });
    act(() => {
      vi.advanceTimersByTime(10000);
    });
    expect(container.querySelector('[role="status"]')).not.toBeNull();

    act(() => {
      item.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
    });
    act(() => {
      vi.advanceTimersByTime(1999);
    });
    expect(container.querySelector('[role="status"]')).not.toBeNull();
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(container.querySelector('[role="status"]')).toBeNull();
  });

  it("pauses for as long as the dismiss button has focus", () => {
    vi.useFakeTimers({ toFake: [...TIMER_FAKES] });
    render(<Toaster />);
    act(() => {
      toast.info("hi");
    });

    const button = container.querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')!;
    act(() => {
      button.focus();
    });
    act(() => {
      vi.advanceTimersByTime(20000);
    });
    expect(container.querySelector('[role="status"]')).not.toBeNull();
  });

  it("clears the timer when dismissed early", () => {
    vi.useFakeTimers({ toFake: [...TIMER_FAKES] });
    render(<Toaster />);
    act(() => {
      toast.error("boom");
    });

    const button = container.querySelector<HTMLButtonElement>('button[aria-label="Dismiss"]')!;
    act(() => {
      button.click();
    });
    expect(container.querySelector('[role="alert"]')).toBeNull();
    act(() => {
      vi.advanceTimersByTime(20000);
    });
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });
});
