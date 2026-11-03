/**
 * Lane culling for large timelines.
 *
 * A timeline draws one lane per node — a column of day cells when dates run
 * down the page, a row of them when they run across — so a 2000-node project
 * is 2000 lanes on every day. Past a few dozen lanes only the ones near the
 * view are mounted, read from the same culling window the graph uses
 * (useViewWindow), and each run of lanes left out is one empty element of
 * the same size, so every lane that is drawn stays exactly where it was.
 */

import type { ViewWindow } from "../canvas/viewportCulling";

/** Timelines with fewer lanes than this draw every one of them. */
export const TIMELINE_CULL_MIN_LANES = 40;

export type LaneSlot =
  | { kind: "lane"; index: number }
  /** `count` lanes from `from` on, not mounted. */
  | { kind: "skip"; from: number; count: number };

/** The stretch of the lane axis the window covers, in board units. */
export function laneSpan(
  view: ViewWindow,
  axis: "x" | "y",
): { from: number; to: number } | "measuring" | null {
  if (view === null || view === "measuring") return view;
  return axis === "x" ? { from: view.left, to: view.right } : { from: view.top, to: view.bottom };
}

/**
 * The lanes to mount, in order, with a skip for each run left out. `start` is
 * where lane 0 begins along the axis and `size` how much each lane takes.
 * With no span every lane is drawn; while the view is still being measured
 * only the pinned ones are — the lanes something holds (a drag, the
 * selection, the open document), which stay mounted wherever the view goes.
 */
export function laneSlots(
  count: number,
  span: { from: number; to: number } | "measuring" | null,
  start: number,
  size: number,
  pinned: Iterable<number> = [],
): LaneSlot[] {
  const shown = new Set<number>();
  if (span === null) {
    return Array.from({ length: count }, (_, index) => ({ kind: "lane" as const, index }));
  }
  if (span !== "measuring" && size > 0) {
    const first = Math.max(0, Math.floor((span.from - start) / size));
    const last = Math.min(count - 1, Math.ceil((span.to - start) / size) - 1);
    for (let index = first; index <= last; index++) shown.add(index);
  }
  for (const index of pinned) if (index >= 0 && index < count) shown.add(index);
  const slots: LaneSlot[] = [];
  let next = 0;
  for (const index of [...shown].sort((a, b) => a - b)) {
    if (index > next) slots.push({ kind: "skip", from: next, count: index - next });
    slots.push({ kind: "lane", index });
    next = index + 1;
  }
  if (next < count) slots.push({ kind: "skip", from: next, count: count - next });
  return slots;
}
