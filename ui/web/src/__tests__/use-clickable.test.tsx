/**
 * Tests for the keyboard-activation helper used by clickable rows/divs.
 *
 * NOTE: @testing-library/react is not installed in this project, so components are
 * rendered with react-dom/client + act (jsdom), following the existing convention.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ReactElement } from "react";
import { getClickableProps, useClickable, type ClickableEvent } from "@/hooks/use-clickable";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

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

function render(ui: ReactElement) {
  act(() => {
    root.render(ui);
  });
}

function press(target: Element, key: string): KeyboardEvent {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

type Activate = (event: ClickableEvent) => void;

function HookDiv({ onActivate }: { onActivate: Activate }) {
  return (
    <div data-testid="target" {...useClickable(onActivate)}>
      <button type="button" data-testid="nested">
        nested
      </button>
    </div>
  );
}

function HelperDiv({ onActivate }: { onActivate: Activate }) {
  return (
    <div data-testid="target" {...getClickableProps(onActivate)}>
      <button type="button" data-testid="nested">
        nested
      </button>
    </div>
  );
}

describe("getClickableProps", () => {
  it("returns button semantics with handlers", () => {
    const props = getClickableProps(() => {});
    expect(props.role).toBe("button");
    expect(props.tabIndex).toBe(0);
    expect(typeof props.onClick).toBe("function");
    expect(typeof props.onKeyDown).toBe("function");
  });
});

const variants: Array<{ name: string; Component: (props: { onActivate: Activate }) => ReactElement }> = [
  { name: "getClickableProps", Component: HelperDiv },
  { name: "useClickable", Component: HookDiv },
];

for (const { name, Component } of variants) {
  describe(name, () => {
    it("renders role=button and tabIndex=0", () => {
      render(<Component onActivate={() => {}} />);
      const target = container.querySelector<HTMLElement>('[data-testid="target"]');
      expect(target?.getAttribute("role")).toBe("button");
      expect(target?.getAttribute("tabindex")).toBe("0");
    });

    it("activates on Enter and prevents default (blocks sensor drag-start)", () => {
      const onActivate = vi.fn();
      render(<Component onActivate={onActivate} />);
      const target = container.querySelector('[data-testid="target"]')!;
      const event = press(target, "Enter");
      expect(onActivate).toHaveBeenCalledTimes(1);
      expect(event.defaultPrevented).toBe(true);
    });

    it("activates on Space and prevents scrolling", () => {
      const onActivate = vi.fn();
      render(<Component onActivate={onActivate} />);
      const target = container.querySelector('[data-testid="target"]')!;
      const event = press(target, " ");
      expect(onActivate).toHaveBeenCalledTimes(1);
      expect(event.defaultPrevented).toBe(true);
    });

    it("ignores other keys", () => {
      const onActivate = vi.fn();
      render(<Component onActivate={onActivate} />);
      press(container.querySelector('[data-testid="target"]')!, "a");
      expect(onActivate).not.toHaveBeenCalled();
    });

    it("activates on click", () => {
      const onActivate = vi.fn();
      render(<Component onActivate={onActivate} />);
      act(() => {
        container.querySelector<HTMLElement>('[data-testid="target"]')!.click();
      });
      expect(onActivate).toHaveBeenCalledTimes(1);
    });

    it("does not hijack Enter/Space from nested controls", () => {
      const onActivate = vi.fn();
      render(<Component onActivate={onActivate} />);
      const nested = container.querySelector('[data-testid="nested"]')!;
      press(nested, "Enter");
      press(nested, " ");
      expect(onActivate).not.toHaveBeenCalled();
    });
  });
}
