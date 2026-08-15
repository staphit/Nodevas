/**
 * Touch equivalents for the interactions this app expresses with a mouse.
 *
 * The desktop UI leans on gestures a finger does not have: right click for
 * every context menu, Alt+wheel for zoom. Each is given the touch idiom that
 * means the same thing — long press for "menu about this thing", pinch for
 * zoom — while the mouse paths stay exactly as they were. A hybrid device (an
 * iPad with a trackpad, a laptop with a touchscreen) gets both, because the
 * choice is made per pointer event rather than once per session.
 *
 * Long press is installed once, at the root, and works by dispatching a real
 * `contextmenu` event on whatever was pressed. Every menu in this app is
 * already an `onContextMenu` handler — cards, edges, logic gates, tree rows,
 * timeline cells, tabs — and they keep working untouched, including the ones
 * that stop propagation to claim the press from a parent. The alternative, a
 * hook threaded through every one of those call sites, would have to
 * re-implement that dispatch by hand and would drift the first time a menu was
 * added without it.
 */

import { useEffect, useState } from "react";

/**
 * How long a finger must rest before it counts as a menu request. Matches
 * iOS's own long-press threshold, so a press that opens a Nodevas menu and one
 * that opens a system menu never disagree about when they fired.
 */
export const LONG_PRESS_MS = 500;

/**
 * How far a finger may wander and still be a press rather than a drag. A
 * finger is never as still as a mouse, so zero tolerance makes the menu
 * unreachable; too much swallows the start of a card drag.
 */
export const LONG_PRESS_SLOP_PX = 10;

/** A mouse already has a right button, so long press is for everything else. */
function isTouchLike(pointerType: string): boolean {
  return pointerType !== "mouse";
}

/**
 * Text fields get the system's long press — selection, the loupe, Paste — not
 * ours. Overriding it there would take away the only way to select text on a
 * phone and give back a menu about the node behind the field.
 */
function wantsSystemLongPress(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  return Boolean(
    target.closest("input, textarea, [contenteditable=''], [contenteditable='true'], .cm-content"),
  );
}

/**
 * Installs long-press-opens-the-context-menu for the whole document.
 *
 * Call once, at the app root.
 */
export function useTouchContextMenu(): void {
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let press: { pointerId: number; x: number; y: number; target: Element } | null = null;
    // Set the moment a menu opens and cleared on the next press: the finger
    // that opened the menu still lifts, and that lift would otherwise land as
    // a tap on whatever is now underneath.
    let swallowNextClick = false;
    let activePointers = 0;

    const clear = () => {
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
      press = null;
    };

    const onPointerDown = (event: PointerEvent) => {
      activePointers += 1;
      swallowNextClick = false;
      if (!isTouchLike(event.pointerType) || wantsSystemLongPress(event.target)) {
        clear();
        return;
      }
      // A second finger means a pinch, not a press.
      if (activePointers > 1) {
        clear();
        return;
      }
      const target = event.target instanceof Element ? event.target : null;
      if (!target) return;
      press = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, target };
      timer = setTimeout(() => {
        timer = null;
        const pressed = press;
        press = null;
        if (!pressed) return;
        swallowNextClick = true;
        // A real, bubbling event, so a handler that stops propagation still
        // claims the press from its parent exactly as it does for a mouse.
        pressed.target.dispatchEvent(
          new MouseEvent("contextmenu", {
            bubbles: true,
            cancelable: true,
            clientX: pressed.x,
            clientY: pressed.y,
            // Buttons a right click would report, for handlers that check.
            button: 2,
            buttons: 2,
          }),
        );
      }, LONG_PRESS_MS);
    };

    const onPointerMove = (event: PointerEvent) => {
      if (!press || press.pointerId !== event.pointerId) return;
      const moved = Math.abs(event.clientX - press.x) + Math.abs(event.clientY - press.y);
      // Past the slop this is a drag, and a drag must not sprout a menu
      // halfway through.
      if (moved > LONG_PRESS_SLOP_PX) clear();
    };

    const onPointerEnd = (event: PointerEvent) => {
      activePointers = Math.max(0, activePointers - 1);
      if (press && press.pointerId !== event.pointerId) return;
      clear();
    };

    const onClickCapture = (event: MouseEvent) => {
      if (!swallowNextClick) return;
      swallowNextClick = false;
      event.preventDefault();
      event.stopPropagation();
    };

    // Scrolling anywhere ends the press: the content under the finger has
    // moved, so a menu about it would be about the wrong thing.
    const onScroll = () => clear();

    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("pointermove", onPointerMove, true);
    document.addEventListener("pointerup", onPointerEnd, true);
    document.addEventListener("pointercancel", onPointerEnd, true);
    document.addEventListener("click", onClickCapture, true);
    document.addEventListener("scroll", onScroll, true);
    return () => {
      clear();
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("pointermove", onPointerMove, true);
      document.removeEventListener("pointerup", onPointerEnd, true);
      document.removeEventListener("pointercancel", onPointerEnd, true);
      document.removeEventListener("click", onClickCapture, true);
      document.removeEventListener("scroll", onScroll, true);
    };
  }, []);
}

const COARSE_POINTER_QUERY = "(pointer: coarse)";
const ANY_COARSE_POINTER_QUERY = "(any-pointer: coarse)";

/** Shared subscription body for the two pointer-capability hooks below. */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const list = window.matchMedia(query);
    const onChange = (event: MediaQueryListEvent) => setMatches(event.matches);
    setMatches(list.matches);
    list.addEventListener("change", onChange);
    return () => list.removeEventListener("change", onChange);
  }, [query]);

  return matches;
}

/**
 * Whether the device has a touch screen at all, whatever it is mainly driven
 * with.
 *
 * This is the right question for "should the touch-only affordance exist" —
 * an iPad with a Magic Keyboard reports a *primary* fine pointer, and a
 * touchscreen laptop always does, but on both of them a finger is still going
 * to land on the board and needs somewhere to reach the gestures that a
 * modifier key otherwise spells. Offering the control costs three small
 * buttons; withholding it costs the gesture entirely.
 */
export function useTouchCapable(): boolean {
  return useMediaQuery(ANY_COARSE_POINTER_QUERY);
}

/**
 * Whether the primary input is a finger, kept current as that changes: an iPad
 * gains a fine pointer the moment a trackpad is paired and loses it again when
 * the case comes off, and a control that only appears for touch has to appear
 * and disappear with it.
 *
 * Used only to decide whether to *offer* something — never to choose which
 * gesture handler runs, since a device can have both and the answer would be
 * wrong for one of them.
 */
export function useCoarsePointer(): boolean {
  return useMediaQuery(COARSE_POINTER_QUERY);
}
