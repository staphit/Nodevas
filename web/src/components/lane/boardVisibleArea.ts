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
 * right edge for a side drawer, the bottom for a sheet.
 */
export function visibleArea(board: Area, overlays: readonly Area[]): Area {
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
    if (width(best) >= MIN_VISIBLE_W && height(best) >= MIN_VISIBLE_H) visible = best;
  }
  return visible;
}

/**
 * Where, in the board's own client coordinates, a card should be centred so
 * it lands in the middle of what is showing.
 */
export function visibleBoardCentre(board: HTMLElement): { x: number; y: number } {
  const rect = board.getBoundingClientRect();
  const area: Area = {
    left: rect.left,
    top: rect.top,
    // The client size, not the rect's: the scrollbars are not board.
    right: rect.left + board.clientWidth,
    bottom: rect.top + board.clientHeight,
  };
  const overlays = Array.from(
    board.ownerDocument.querySelectorAll<HTMLElement>(BOARD_OVERLAY_SELECTOR),
  )
    .filter((element) => !board.contains(element))
    .map((element) => element.getBoundingClientRect());
  const visible = visibleArea(area, overlays);
  return {
    x: (visible.left + visible.right) / 2 - rect.left,
    y: (visible.top + visible.bottom) / 2 - rect.top,
  };
}
