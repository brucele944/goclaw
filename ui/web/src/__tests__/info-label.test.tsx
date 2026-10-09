/**
 * Tests for InfoLabel: control association and a keyboard-reachable tooltip trigger.
 *
 * NOTE: @testing-library/react is not installed in this project, so components are
 * rendered with react-dom/client + act (jsdom), following the existing convention.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ReactElement } from "react";
import { InfoLabel } from "@/components/shared/info-label";

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

function render(ui: ReactElement) {
  act(() => {
    root.render(ui);
  });
}

describe("InfoLabel", () => {
  it("associates the label with the control and forwards label clicks to it", () => {
    render(
      <>
        <InfoLabel tip="Model identifier" htmlFor="model-field">
          Model
        </InfoLabel>
        <input id="model-field" data-testid="input" />
      </>,
    );

    const label = container.querySelector("label")!;
    const input = container.querySelector<HTMLInputElement>('[data-testid="input"]')!;

    expect(label.getAttribute("for")).toBe("model-field");
    expect(label.control).toBe(input);

    const onClick = vi.fn();
    input.addEventListener("click", onClick);
    act(() => {
      label.click();
    });
    // The label forwards activation to its control; browsers then focus it.
    // jsdom fires the forwarded click but does not implement focus-on-click.
    expect(onClick).toHaveBeenCalledTimes(1);

    act(() => {
      input.focus();
    });
    expect(document.activeElement).toBe(input);
  });

  it("omits the for attribute when htmlFor is not provided", () => {
    render(<InfoLabel tip="Anything">Unbound</InfoLabel>);
    const label = container.querySelector("label")!;
    expect(label.hasAttribute("for")).toBe(false);
  });

  it("renders the tooltip trigger as a focusable button with an accessible name", () => {
    render(<InfoLabel tip="Helpful description">Field</InfoLabel>);

    const trigger = container.querySelector("button")!;
    expect(trigger.getAttribute("type")).toBe("button");
    expect(trigger.getAttribute("aria-label")).toBe("Helpful description");

    trigger.focus();
    expect(document.activeElement).toBe(trigger);
  });

  it("hides the decorative icon from assistive tech", () => {
    render(<InfoLabel tip="Helpful description">Field</InfoLabel>);
    const icon = container.querySelector("button svg")!;
    expect(icon.getAttribute("aria-hidden")).toBe("true");
  });
});
