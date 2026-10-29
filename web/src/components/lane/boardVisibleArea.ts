/**
 * The part of the board a reader can actually see.
 *
 * The node drawer is laid over the board rather than beside it (on a phone it
 * is a sheet over the bottom), so "the middle of the board" can be under it.
 * Anything that centres a card for the reader — a Ctrl+K or link jump — asks
 * this instead. Panels other than the drawer that sit over the board opt in
 * with a `data-board-overlay` attribute.
 */

export const BOARD_OVERLAY_SELECTOR = ".drawer, [data-board-overlay]";

export interface Area {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/**
 * Below this a cut is not taken: a panel that would leave only a sliver (a
 * phone's full-screen panel, say) is better ignored than centred into.
 */
const MIN_VISIBLE_W = 160;
const MIN_VISIBLE_H = 100;

const width = (area: Area) => Math.max(0, area.right - area.left);
const height = (area: Area) => Math.max(0, area.bottom - area.top);

/**
 * `board` with each overlay cut off it, in viewport coordinates. An overlay
 * is removed by moving whichever one edge keeps the most of the board — the
 * right edge for a side drawer, the bottom for a sheet. A cut that would leave
 * less than `least` is not taken; with a least of 0 every cut is, and what is
 * left can be a sliver or nothing.
 */
export function visibleArea(
  board: Area,
  overlays: readonly Area[],
  least: { width: number; height: number } = { width: MIN_VISIBLE_W, height: MIN_VISIBLE_H },
): Area {
  let visible = { ...board };
  for (const overlay of overlays) {
    const covered = {
      left: Math.max(visible.left, overlay.left),
      top: Math.max(visible.top, overlay.top),
      right: Math.min(visible.right, overlay.right),
      bottom: Math.min(visible.bottom, overlay.bottom),
    };
    if (width(covered) <= 0 || height(covered) <= 0) continue;
    const cuts: Area[] = [
      { ...visible, right: covered.left },
      { ...visible, left: covered.right },
      { ...visible, bottom: covered.top },
      { ...visible, top: covered.bottom },
    ];
    const best = cuts.reduce((kept, cut) =>
      width(cut) * height(cut) > width(kept) * height(kept) ? cut : kept,
    );
    if (width(best) >= least.width && height(best) >= least.height) visible = best;
  }
  return visible;
}

/**
 * How far `visible` sits inside `outer` on each side, never negative: the
 * padding that keeps something laid out in `outer` within what shows.
 */
export function insetsWithin(outer: Area, visible: Area): Area {
  return {
    left: Math.max(0, Math.round(visible.left - outer.left)),
    top: Math.max(0, Math.round(visible.top - outer.top)),
    right: Math.max(0, Math.round(outer.right - visible.right)),
    bottom: Math.max(0, Math.round(outer.bottom - visible.bottom)),
  };
}

/**
 * The overlays laid over the board from outside `board`, where they will come
 * to rest.
 */
export function boardOverlayRects(board: HTMLElement): Area[] {
  return Array.from(board.ownerDocument.querySelectorAll<HTMLElement>(BOARD_OVERLAY_SELECTOR))
    .filter((element) => !board.contains(element))
    .map(restingRect);
}

/**
 * An element's rect without its current translation. The drawer slides in
 * (and a phone's sheet slides up) while a jump is being answered, and the
 * card should be centred against where it comes to rest.
 */
export function restingRect(element: HTMLElement): Area {
  const rect = element.getBoundingClientRect();
  const transform = getComputedStyle(element).transform;
  if (!transform || transform === "none" || typeof DOMMatrixReadOnly === "undefined") {
    return rect;
  }
  try {
    const { e, f } = new DOMMatrixReadOnly(transform);
    return { left: rect.left - e, right: rect.right - e, top: rect.top - f, bottom: rect.bottom - f };
  } catch {
    return rect;
  }
}

/** The part of `board` that is showing, in viewport coordinates. */
export function visibleBoardArea(board: HTMLElement): Area {
  const rect = board.getBoundingClientRect();
  const area: Area = {
    left: rect.left,
    top: rect.top,
    // The client size, not the rect's: the scrollbars are not board.
    right: rect.left + board.clientWidth,
    bottom: rect.top + board.clientHeight,
  };
  return visibleArea(area, boardOverlayRects(board));
}

/**
 * Where, in the board's own client coordinates, a card should be centred so
 * it lands in the middle of what is showing.
 */
export function visibleBoardCentre(board: HTMLElement): { x: number; y: number } {
  const rect = board.getBoundingClientRect();
  const visible = visibleBoardArea(board);
  return {
    x: (visible.left + visible.right) / 2 - rect.left,
    y: (visible.top + visible.bottom) / 2 - rect.top,
  };
}
