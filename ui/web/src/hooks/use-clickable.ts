import { useCallback, useMemo } from "react";
import type { KeyboardEvent, MouseEvent } from "react";

/** Activation event passed to the handler: mouse click or Enter/Space key press. */
export type ClickableEvent = MouseEvent<HTMLElement> | KeyboardEvent<HTMLElement>;

export type ClickableActivate = (event: ClickableEvent) => void;

/** Props that make a non-interactive element behave like a button. */
export interface ClickableProps {
  role: "button";
  tabIndex: 0;
  onClick: (event: MouseEvent<HTMLElement>) => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
}

function onKeyActivate(event: KeyboardEvent<HTMLElement>, onActivate: ClickableActivate): void {
  // Nested controls (buttons, inputs, links) keep their native behaviour.
  if (event.target !== event.currentTarget) return;
  if (event.key !== "Enter" && event.key !== " ") return;
  // Space would scroll the page. Enter has no native default on a div, but dnd-kit's
  // KeyboardSensor starts a drag from Enter/Space and only skips events whose
  // `defaultPrevented` is set — without this, activating a draggable row would also
  // begin a keyboard drag and the next keypress could move the item.
  event.preventDefault();
  onActivate(event);
}

/**
 * Non-hook form for call sites that cannot call hooks (e.g. rows rendered inside
 * `.map`). Spread the result onto the clickable element.
 */
export function getClickableProps(onActivate: ClickableActivate): ClickableProps {
  return {
    role: "button",
    tabIndex: 0,
    onClick: (event) => onActivate(event),
    onKeyDown: (event) => onKeyActivate(event, onActivate),
  };
}

/** Hook form with stable identities; returns the same props as `getClickableProps`. */
export function useClickable(onActivate: ClickableActivate): ClickableProps {
  const onClick = useCallback(
    (event: MouseEvent<HTMLElement>) => onActivate(event),
    [onActivate],
  );
  const onKeyDown = useCallback(
    (event: KeyboardEvent<HTMLElement>) => onKeyActivate(event, onActivate),
    [onActivate],
  );
  return useMemo(() => ({ role: "button", tabIndex: 0, onClick, onKeyDown }), [onClick, onKeyDown]);
}
