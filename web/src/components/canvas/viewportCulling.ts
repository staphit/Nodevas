/**
 * Viewport culling for large boards.
 *
 * Past a few hundred cards the board mounts only the ones near what the user
 * can see: the visible part of the board plus a margin, snapped outward to a
 * coarse grid so a small pan changes nothing and a long one swaps cards a
 * strip at a time. Everything here is pure maths in board units; the hook that
 * reads the scroll position is useViewWindow, and GraphCanvas decides which
 * cards are kept whatever the window says.
 */

import { ROW_H, type Col, type Point, type Rect } from "./geometry";

/** Boards with fewer cards than this draw every one of them, as they always have. */
export const CULL_MIN_CARDS = 300;
/** How far past each visible edge cards stay mounted, in board units. */
export const CULL_MARGIN = 320;
/** The window's edges snap outward to multiples of this. */
const CULL_STEP = 320;

/**
 * What the board is culled to. A rectangle in board units; "measuring" when
 * culling applies but the view has not been read yet (only the pinned cards
 * are drawn, for the one render before the layout effect measures); null when
 * every card is drawn.
 */
export type ViewWindow = Rect | "measuring" | null;

/**
 * The window for a board scrolled to `scroll` (CSS pixels of the scrolling
 * element) showing `viewport` at `zoom`. Null when the board has no size to
 * cull to — hidden, collapsed, or a test environment without layout.
 */
export function viewWindowFor(
  scroll: { left: number; top: number },
  viewport: { width: number; height: number },
  zoom: number,
): Rect | null {
  if (!(viewport.width > 0 && viewport.height > 0 && zoom > 0)) return null;
  const left = scroll.left / zoom;
  const top = scroll.top / zoom;
  const right = left + viewport.width / zoom;
  const bottom = top + viewport.height / zoom;
  return {
    left: Math.floor((left - CULL_MARGIN) / CULL_STEP) * CULL_STEP,
    top: Math.floor((top - CULL_MARGIN) / CULL_STEP) * CULL_STEP,
    right: Math.ceil((right + CULL_MARGIN) / CULL_STEP) * CULL_STEP,
    bottom: Math.ceil((bottom + CULL_MARGIN) / CULL_STEP) * CULL_STEP,
  };
}

export function sameViewWindow(a: ViewWindow, b: ViewWindow): boolean {
  if (a === b) return true;
  if (!a || !b || a === "measuring" || b === "measuring") return false;
  return a.left === b.left && a.top === b.top && a.right === b.right && a.bottom === b.bottom;
}

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return a.left <= b.right && a.right >= b.left && a.top <= b.bottom && a.bottom >= b.top;
}

/** Where a card is drawn, in board units. */
export function cardRect(
  column: Col,
  centerX: (column: Col) => number,
  rowTop: (row: number) => number,
): Rect {
  const x = centerX(column);
  const y = rowTop(column.row) + ROW_H / 2;
  return {
    left: x - column.width / 2,
    right: x + column.width / 2,
    top: y - column.height / 2,
    bottom: y + column.height / 2,
  };
}

/**
 * The box around a set of cards and loose points (a gate's placement, a wire's
 * bend points). A wire inside it may cross the view; a wire whose box misses
 * the view cannot, so testing the box draws every wire that crosses and a few
 * that only come close.
 */
export function boundsOf(rects: Rect[], points: Point[] = []): Rect | null {
  let bounds: Rect | null = null;
  const grow = (left: number, top: number, right: number, bottom: number) => {
    bounds = bounds
      ? {
          left: Math.min(bounds.left, left),
          top: Math.min(bounds.top, top),
          right: Math.max(bounds.right, right),
          bottom: Math.max(bounds.bottom, bottom),
        }
      : { left, top, right, bottom };
  };
  for (const rect of rects) grow(rect.left, rect.top, rect.right, rect.bottom);
  for (const point of points) grow(point.x, point.y, point.x, point.y);
  return bounds;
}
