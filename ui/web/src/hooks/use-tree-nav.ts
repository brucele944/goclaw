import type { KeyboardEvent } from "react";

/**
 * Keyboard navigation for elements rendered as an ARIA tree.
 *
 * Spread `getTreeProps()` on the `role="tree"` container. It makes the tree a
 * single tab stop and moves focus with ArrowUp/ArrowDown, Home and End (roving
 * tabindex). Folders collapse/expand with ArrowLeft/ArrowRight.
 *
 * Items render with `role="treeitem"`, `tabIndex={-1}` and per-node activation
 * props from `@/hooks/use-clickable` (Enter/Space). Folders set `aria-expanded`,
 * selectable nodes set `aria-selected`.
 */
const TREE_ITEM_SELECTOR = '[role="treeitem"]';

function focusItem(items: HTMLElement[], item: HTMLElement): void {
  for (const candidate of items) candidate.tabIndex = -1;
  item.tabIndex = 0;
  item.focus();
}

function onTreeKeyDown(event: KeyboardEvent<HTMLElement>): void {
  const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(TREE_ITEM_SELECTOR));
  if (items.length === 0) return;

  const move = (target: number) => {
    const item = items[Math.min(Math.max(target, 0), items.length - 1)];
    if (item) focusItem(items, item);
  };

  const currentItem =
    event.target instanceof HTMLElement ? event.target.closest<HTMLElement>(TREE_ITEM_SELECTOR) : null;
  const index = currentItem ? items.indexOf(currentItem) : -1;

  switch (event.key) {
    case "ArrowDown":
      event.preventDefault();
      move(index + 1);
      return;
    case "ArrowUp":
      event.preventDefault();
      move(index < 0 ? items.length - 1 : index - 1);
      return;
    case "Home":
      event.preventDefault();
      move(0);
      return;
    case "End":
      event.preventDefault();
      move(items.length - 1);
      return;
    case "ArrowRight":
      if (currentItem?.getAttribute("aria-expanded") === "false") {
        event.preventDefault();
        currentItem.click();
      } else if (currentItem && index >= 0) {
        event.preventDefault();
        move(index + 1);
      }
      return;
    case "ArrowLeft":
      if (currentItem?.getAttribute("aria-expanded") === "true") {
        event.preventDefault();
        currentItem.click();
      }
      return;
    default:
      return;
  }
}

export interface TreeProps {
  role: "tree";
  tabIndex: 0;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
}

/** Props for the `role="tree"` container; see the module comment for item props. */
export function getTreeProps(): TreeProps {
  return { role: "tree", tabIndex: 0, onKeyDown: onTreeKeyDown };
}
