/**
 * Alignment snapping [B-06].
 *
 * Smart guides for the graph canvas: while something is dragged or resized, its
 * edges and centre lines are pulled onto the edges and centre lines of whatever
 * is standing still, and the lines they share are drawn.
 *
 * Everything works in **board pixels with the graph offsets left out** — the
 * space `graph.ui.groups` and `graph.ui.annotations` already store their
 * geometry in. A card's box is derived from its cell the same way
 * `useCanvasDecorations.createGroupFromSelection` derives one, so cards and
 * decorations are directly comparable and no layout is needed here. The offsets
 * cancel out of every delta and are added back only when the guides are drawn.
 *
 * No React, no store — same contract as `geometry.ts`.
 */

import { COL_W, ROW_H, type Col, type Rect } from "./geometry";

/** How close, in *screen* pixels, a line has to be before it pulls. */
export const SNAP_THRESHOLD_PX = 6;

/** How far a guide is drawn past the boxes it belongs to. */
const GUIDE_PAD = 8;

/** Two lines count as the same line within this many board pixels. */
const SAME_LINE = 0.5;

export type SnapKind = "left" | "center" | "right" | "top" | "middle" | "bottom";

/** One line something can be aligned to, and the box it came from. */
export interface SnapTarget {
  value: number;
  rect: Rect;
  kind: SnapKind;
}

/** Every line on the board, per axis, sorted by value. */
export interface SnapTargets {
  x: SnapTarget[];
  y: SnapTarget[];
}

/** A line to draw: `position` on `axis`, spanning `from`..`to` on the other one. */
export interface SnapGuide {
  axis: "x" | "y";
  position: number;
  from: number;
  to: number;
}

export interface SnapResult {
  dx: number;
  dy: number;
  guides: SnapGuide[];
}

export const NO_SNAP_TARGETS: SnapTargets = { x: [], y: [] };

/** The board-pixel box of a card sitting on its cell. */
export function rectOfColumn(column: Col): Rect {
  const centerX = column.index * COL_W + COL_W / 2;
  const centerY = column.row * ROW_H + ROW_H / 2;
  return {
    left: centerX - column.width / 2,
    right: centerX + column.width / 2,
    top: centerY - column.height / 2,
    bottom: centerY + column.height / 2,
  };
}

/** The smallest box containing all of them, or null when there are none. */
export function unionRect(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) return null;
  let { left, right, top, bottom } = rects[0];
  for (const rect of rects) {
    left = Math.min(left, rect.left);
    right = Math.max(right, rect.right);
    top = Math.min(top, rect.top);
    bottom = Math.max(bottom, rect.bottom);
  }
  return { left, right, top, bottom };
}

export function translateRect(rect: Rect, dx: number, dy: number): Rect {
  return {
    left: rect.left + dx,
    right: rect.right + dx,
    top: rect.top + dy,
    bottom: rect.bottom + dy,
  };
}

function linesOf(rect: Rect): { x: [SnapKind, number][]; y: [SnapKind, number][] } {
  return {
    x: [
      ["left", rect.left],
      ["center", (rect.left + rect.right) / 2],
      ["right", rect.right],
    ],
    y: [
      ["top", rect.top],
      ["middle", (rect.top + rect.bottom) / 2],
      ["bottom", rect.bottom],
    ],
  };
}

/** Anything on the board that can be aligned to: a card, a group, a note. */
export interface SnapBox {
  id: string;
  rect: Rect;
}

const NOTHING: ReadonlySet<string> = new Set();

export function snapBoxesOfColumns(columns: readonly Col[]): SnapBox[] {
  return columns.map((column) => ({ id: column.node.id, rect: rectOfColumn(column) }));
}

/**
 * Every line the moving thing may align to.
 *
 * Built when the gesture starts, not on every move: nothing writes to the store
 * while a drag is live, so the boxes cannot shift underneath it. Whatever is
 * moving has to be excluded — its own lines are zero away from its own edges,
 * and would win every time and pin it in place.
 */
export function buildSnapTargets(
  boxes: readonly SnapBox[],
  exclude: ReadonlySet<string> = NOTHING,
): SnapTargets {
  const x: SnapTarget[] = [];
  const y: SnapTarget[] = [];
  for (const box of boxes) {
    if (exclude.has(box.id)) continue;
    const lines = linesOf(box.rect);
    for (const [kind, value] of lines.x) x.push({ value, rect: box.rect, kind });
    for (const [kind, value] of lines.y) y.push({ value, rect: box.rect, kind });
  }
  x.sort((a, b) => a.value - b.value);
  y.sort((a, b) => a.value - b.value);
  return { x, y };
}

/** First index whose value is >= `value`; the array must be sorted. */
function lowerBound(targets: readonly SnapTarget[], value: number): number {
  let low = 0;
  let high = targets.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (targets[mid].value < value) low = mid + 1;
    else high = mid;
  }
  return low;
}

interface AxisSolution {
  delta: number;
  /** Where the line ends up, in final coordinates. */
  position: number;
}

interface Candidate extends AxisSolution {
  /** Centre-to-centre is the alignment people mean, when it is a tie. */
  centerish: boolean;
  /** Last resort, so the same input always gives the same answer. */
  tiebreak: number;
}

/**
 * Nearest line wins. Distance comes first so a box already sitting exactly on
 * an edge is not tugged off it by a centre line a couple of pixels away.
 */
function beats(candidate: Candidate, best: Candidate): boolean {
  const byDistance = Math.abs(candidate.delta) - Math.abs(best.delta);
  if (Math.abs(byDistance) > 1e-9) return byDistance < 0;
  if (candidate.centerish !== best.centerish) return candidate.centerish;
  return candidate.tiebreak < best.tiebreak;
}

function solveAxis(
  probes: readonly [SnapKind, number][],
  targets: readonly SnapTarget[],
  threshold: number,
): AxisSolution | null {
  let best: Candidate | null = null;
  for (const [probeKind, probeValue] of probes) {
    const centerProbe = probeKind === "center" || probeKind === "middle";
    for (let i = lowerBound(targets, probeValue - threshold); i < targets.length; i++) {
      const target = targets[i];
      if (target.value > probeValue + threshold) break;
      const centerTarget = target.kind === "center" || target.kind === "middle";
      const candidate: Candidate = {
        delta: target.value - probeValue,
        position: target.value,
        centerish: centerProbe && centerTarget,
        tiebreak: target.rect.left + target.rect.top,
      };
      if (!best || beats(candidate, best)) best = candidate;
    }
  }
  return best;
}

function guideFor(
  axis: "x" | "y",
  position: number,
  moving: Rect,
  targets: readonly SnapTarget[],
): SnapGuide {
  const along = (rect: Rect): [number, number] =>
    axis === "x" ? [rect.top, rect.bottom] : [rect.left, rect.right];
  let [from, to] = along(moving);
  for (let i = lowerBound(targets, position - SAME_LINE); i < targets.length; i++) {
    const target = targets[i];
    if (target.value > position + SAME_LINE) break;
    const [start, end] = along(target.rect);
    from = Math.min(from, start);
    to = Math.max(to, end);
  }
  return { axis, position, from: from - GUIDE_PAD, to: to + GUIDE_PAD };
}

/**
 * Pulls a box that has already been offset by the raw drag delta onto the
 * nearest lines, and reports the lines it landed on.
 *
 * `threshold` is in board pixels: divide the screen distance by the zoom so the
 * magnet feels the same at every scale. The two axes are solved separately —
 * lining up horizontally must not drag the box sideways.
 */
export function solveSnap(
  bounds: Rect,
  dx: number,
  dy: number,
  targets: SnapTargets,
  threshold: number,
): SnapResult {
  if (threshold <= 0 || (targets.x.length === 0 && targets.y.length === 0)) {
    return { dx, dy, guides: [] };
  }
  const moved = translateRect(bounds, dx, dy);
  const lines = linesOf(moved);
  const solvedX = solveAxis(lines.x, targets.x, threshold);
  const solvedY = solveAxis(lines.y, targets.y, threshold);

  const snapped = translateRect(moved, solvedX?.delta ?? 0, solvedY?.delta ?? 0);
  const guides: SnapGuide[] = [];
  if (solvedX) guides.push(guideFor("x", solvedX.position, snapped, targets.x));
  if (solvedY) guides.push(guideFor("y", solvedY.position, snapped, targets.y));

  return {
    dx: dx + (solvedX?.delta ?? 0),
    dy: dy + (solvedY?.delta ?? 0),
    guides,
  };
}

/**
 * Snapping for a resize: only the edge the handle drags is a probe.
 *
 * `dirX`/`dirY` come from `CARD_HANDLES`; a zero means that axis is not being
 * resized. The anchor says what stays put — a node card is pinned at its centre
 * (`left = centerX - width / 2`), so both edges move and the size has to change
 * by twice the pull; a group box is pinned at its top-left corner, so the size
 * changes by the pull itself. Returns the extra width/height, still unclamped.
 */
export function solveResizeSnap(
  bounds: Rect,
  dirX: number,
  dirY: number,
  targets: SnapTargets,
  threshold: number,
  anchor: "center" | "start" = "center",
): { dWidth: number; dHeight: number; guides: SnapGuide[] } {
  if (threshold <= 0) return { dWidth: 0, dHeight: 0, guides: [] };
  const spread = anchor === "center" ? 2 : 1;
  const guides: SnapGuide[] = [];
  let dWidth = 0;
  let dHeight = 0;

  if (dirX !== 0) {
    const kind: SnapKind = dirX > 0 ? "right" : "left";
    const edge = dirX > 0 ? bounds.right : bounds.left;
    const solved = solveAxis([[kind, edge]], targets.x, threshold);
    if (solved) {
      dWidth = solved.delta * dirX * spread;
      guides.push(guideFor("x", solved.position, bounds, targets.x));
    }
  }
  if (dirY !== 0) {
    const kind: SnapKind = dirY > 0 ? "bottom" : "top";
    const edge = dirY > 0 ? bounds.bottom : bounds.top;
    const solved = solveAxis([[kind, edge]], targets.y, threshold);
    if (solved) {
      dHeight = solved.delta * dirY * spread;
      guides.push(guideFor("y", solved.position, bounds, targets.y));
    }
  }
  return { dWidth, dHeight, guides };
}
