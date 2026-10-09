/**
 * Tests for the Sheet primitive: dialog semantics, Escape to close, and focus
 * restoration to the element that was focused before the sheet opened.
 *
 * NOTE: @testing-library/react is not installed in this project, so components are
 * rendered with react-dom/client + act (jsdom), following the existing convention.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Sheet, SheetContent, SheetTrigger } from "@/components/ui/sheet";
import "@/i18n";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

// jsdom has no ResizeObserver; Radix primitives use it in a layout effect.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Object.assign(globalThis, { ResizeObserver: ResizeObserverStub });

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function Harness({ side }: { side?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger>Open drawer</SheetTrigger>
      <SheetContent side={side} title="Navigation" description="Primary navigation">
        <p>Drawer body</p>
      </SheetContent>
    </Sheet>
  );
}

function openSheet(side?: "left" | "right") {
  act(() => root.render(<Harness side={side} />));
  const trigger = container.querySelector("button")!;
  trigger.focus();
  act(() => trigger.click());
  return trigger;
}

/** Mirrors real usage: the sheet is opened by a button outside the Radix tree. */
function ProgrammaticHarness() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open sidebar
      </button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent title="Navigation">
          <p>Drawer body</p>
        </SheetContent>
      </Sheet>
    </>
  );
}

function dialog(): HTMLElement | null {
  return document.body.querySelector<HTMLElement>('[role="dialog"]');
}

describe("Sheet", () => {
  it("renders a focus-trapped modal dialog labelled and described by its hidden title/description", () => {
    const trigger = openSheet("left");

    const content = dialog();
    expect(content).not.toBeNull();
    expect(content!.getAttribute("aria-modal")).toBe("true");
    expect(content!.dataset.state).toBe("open");

    const labelledBy = content!.getAttribute("aria-labelledby")!;
    expect(document.getElementById(labelledBy)!.textContent).toBe("Navigation");
    const describedBy = content!.getAttribute("aria-describedby")!;
    expect(document.getElementById(describedBy)!.textContent).toBe("Primary navigation");

    // Focus moved into the dialog.
    expect(content!.contains(document.activeElement)).toBe(true);
    // The trigger is still in the tree and can be restored to later.
    expect(trigger.isConnected).toBe(true);
  });

  it("omits aria-describedby when no description is provided", () => {
    act(() =>
      root.render(
        <Sheet open>
          <SheetContent title="Navigation">
            <p>Drawer body</p>
          </SheetContent>
        </Sheet>,
      ),
    );
    expect(dialog()!.hasAttribute("aria-describedby")).toBe(false);
    expect(dialog()!.getAttribute("aria-modal")).toBe("true");
  });

  it("closes on Escape and returns focus to the element focused before it opened", async () => {
    const trigger = openSheet();
    expect(dialog()).not.toBeNull();

    act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });

    expect(dialog()).toBeNull();
    // Radix restores focus in a macrotask after unmount.
    await vi.waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it("restores focus to the activating button when no SheetTrigger is rendered", async () => {
    act(() => root.render(<ProgrammaticHarness />));
    const activator = container.querySelector("button")!;
    activator.focus();
    act(() => activator.click());
    expect(dialog()).not.toBeNull();

    act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });

    expect(dialog()).toBeNull();
    await vi.waitFor(() => expect(document.activeElement).toBe(activator));
  });
});
