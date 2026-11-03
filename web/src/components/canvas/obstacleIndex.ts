/**
 * The board's obstacles bucketed on a coarse grid, so routing one wire looks
 * at the few cards around it instead of every card on the board.
 *
 * Routing a gate or a bend only ever depends on the obstacles that meet a box
 * around the points it tests, so asking the index for those — in board order,
 * since the routing takes the first obstacle a point is inside — gives the
 * same answer as testing the whole board.
 */

import {
  WIRE_CLEARANCE,
  constrainPointOutsideRects,
  expandRect,
  nearestClearRatio,
  pointInsideRect,
  type Point,
  type Rect,
} from "./geometry";

/** Grid cell size in board units: a couple of cards each way. */
const CELL = 256;

export interface ObstacleIndex {
  /** Every obstacle, in board order. */
  readonly rects: readonly Rect[];
  /** The obstacles meeting `box` (edges count), in board order. */
  near(box: Rect): Rect[];
}

export function obstacleIndex(rects: readonly Rect[]): ObstacleIndex {
  const cells = new Map<string, number[]>();
  const span = (low: number, high: number) => [
    Math.floor(low / CELL),
    Math.floor(high / CELL),
  ];
  rects.forEach((rect, at) => {
    const [x0, x1] = span(rect.left, rect.right);
    const [y0, y1] = span(rect.top, rect.bottom);
    for (let x = x0; x <= x1; x++) {
      for (let y = y0; y <= y1; y++) {
        const key = `${x},${y}`;
        const bucket = cells.get(key);
        if (bucket) bucket.push(at);
        else cells.set(key, [at]);
      }
    }
  });
  // Which query last saw each obstacle, so one spanning cells is taken once.
  const seen = new Uint32Array(rects.length);
  let query = 0;
  return {
    rects,
    near(box) {
      if (!(box.left <= box.right && box.top <= box.bottom)) return [];
      const [x0, x1] = span(box.left, box.right);
      const [y0, y1] = span(box.top, box.bottom);
      // A box wider than the board: the whole list is cheaper than the cells.
      if ((x1 - x0 + 1) * (y1 - y0 + 1) > cells.size) {
        return rects.filter((rect) => meets(rect, box));
      }
      query = (query + 1) >>> 0 || 1;
      const found: number[] = [];
      for (let x = x0; x <= x1; x++) {
        for (let y = y0; y <= y1; y++) {
          for (const at of cells.get(`${x},${y}`) ?? []) {
            if (seen[at] === query) continue;
            seen[at] = query;
            if (meets(rects[at], box)) found.push(at);
          }
        }
      }
      found.sort((a, b) => a - b);
      return found.map((at) => rects[at]);
    },
  };
}

function meets(a: Rect, b: Rect): boolean {
  return a.left <= b.right && a.right >= b.left && a.top <= b.bottom && a.bottom >= b.top;
}

/** constrainPointOutsideRects against every obstacle in the index. */
export function constrainPointOutsideIndex(
  point: Point,
  index: ObstacleIndex,
  clearance = WIRE_CLEARANCE,
): Point {
  let next = { ...point };
  for (let pass = 0; pass < index.rects.length; pass++) {
    // An obstacle grown by the clearance holds the point exactly when the
    // obstacle meets the point grown by it.
    const obstacle = index
      .near(expandRect({ left: next.x, right: next.x, top: next.y, bottom: next.y }, clearance, clearance))
      .find((rect) => pointInsideRect(next, expandRect(rect, clearance, clearance)));
    if (!obstacle) break;
    // One step of the same push, off this obstacle.
    next = constrainPointOutsideRects(next, [obstacle], clearance);
  }
  return next;
}

/** nearestClearRatio against every obstacle in the index. */
export function nearestClearRatioInIndex(
  start: Point,
  end: Point,
  preferred: number,
  index: ObstacleIndex,
  horizontalClearance: number,
  verticalClearance: number,
): number {
  // Only an obstacle whose grown box crosses the segment can block a point on
  // it or offer a candidate, and every such obstacle meets the segment's box
  // grown by the same clearance.
  const along = index.near(
    expandRect(
      {
        left: Math.min(start.x, end.x),
        right: Math.max(start.x, end.x),
        top: Math.min(start.y, end.y),
        bottom: Math.max(start.y, end.y),
      },
      horizontalClearance,
      verticalClearance,
    ),
  );
  return nearestClearRatio(
    start,
    end,
    preferred,
    along,
    horizontalClearance,
    verticalClearance,
  );
}

/**
 * The index, noting the box around everything looked up through it. Whatever
 * was routed through it depends on no obstacle outside that box, so a card
 * moving elsewhere leaves the result as it is.
 */
export function watchedIndex(
  index: ObstacleIndex,
): ObstacleIndex & { reach(): Rect | null } {
  let reach: Rect | null = null;
  return {
    rects: index.rects,
    near(box) {
      reach = reach
        ? {
            left: Math.min(reach.left, box.left),
            top: Math.min(reach.top, box.top),
            right: Math.max(reach.right, box.right),
            bottom: Math.max(reach.bottom, box.bottom),
          }
        : { ...box };
      return index.near(box);
    },
    reach: () => reach,
  };
}
