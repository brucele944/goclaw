/**
 * Tests for the ARIA tree keyboard-navigation helper.
 *
 * NOTE: @testing-library/react is not installed in this project, so components are
 * rendered with react-dom/client + act (jsdom), following the existing convention.
 */
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ReactElement } from "react";
import { getTreeProps } from "@/hooks/use-tree-nav";

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

function byTestId(id: string): HTMLElement {
  const element = container.querySelector<HTMLElement>(`[data-testid="${id}"]`);
  if (!element) throw new Error(`missing ${id}`);
  return element;
}

function Tree() {
  const [expanded, setExpanded] = useState(false);
  return (
    <div data-testid="tree" {...getTreeProps()}>
      <div
        data-testid="folder"
        role="treeitem"
        tabIndex={-1}
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        folder
      </div>
      <div data-testid="file-a" role="treeitem" tabIndex={-1}>
        a
      </div>
      <div data-testid="file-b" role="treeitem" tabIndex={-1}>
        b
      </div>
    </div>
  );
}

describe("getTreeProps", () => {
  it("exposes a single tree tab stop", () => {
    const props = getTreeProps();
    expect(props.role).toBe("tree");
    expect(props.tabIndex).toBe(0);
    expect(typeof props.onKeyDown).toBe("function");
  });
});

describe("tree keyboard navigation", () => {
  it("moves focus with Arrow/Home/End and keeps a roving tabindex", () => {
    render(<Tree />);
    const tree = byTestId("tree");
    const folder = byTestId("folder");
    const fileA = byTestId("file-a");
    const fileB = byTestId("file-b");

    const down = press(tree, "ArrowDown");
    expect(down.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(folder);
    expect(folder.tabIndex).toBe(0);
    expect(fileA.tabIndex).toBe(-1);

    press(folder, "ArrowDown");
    expect(document.activeElement).toBe(fileA);
    expect(fileA.tabIndex).toBe(0);
    expect(folder.tabIndex).toBe(-1);

    press(fileA, "End");
    expect(document.activeElement).toBe(fileB);

    press(fileB, "Home");
    expect(document.activeElement).toBe(folder);

    press(fileA, "ArrowUp");
    expect(document.activeElement).toBe(folder);
  });

  it("focuses the last item on ArrowUp from the container", () => {
    render(<Tree />);
    press(byTestId("tree"), "ArrowUp");
    expect(document.activeElement).toBe(byTestId("file-b"));
  });

  it("expands with ArrowRight, collapses with ArrowLeft, and descends when open", () => {
    render(<Tree />);
    const tree = byTestId("tree");
    const folder = byTestId("folder");
    press(tree, "ArrowDown");

    press(folder, "ArrowRight");
    expect(folder.getAttribute("aria-expanded")).toBe("true");

    press(folder, "ArrowRight");
    expect(document.activeElement).toBe(byTestId("file-a"));

    press(byTestId("file-a"), "ArrowLeft");
    expect(document.activeElement).toBe(byTestId("file-a"));

    press(folder, "ArrowLeft");
    expect(folder.getAttribute("aria-expanded")).toBe("false");
  });
});
