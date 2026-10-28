/**
 * Board sizing maths [B-06].
 *
 * How large the scrolling surface is, where logical (0,0) lands inside it, and
 * which grid cell a pointer is over. All of it is a pure function of the grid,
 * the viewport and the zoom, so none of it needs React.
 */

import type { LogicGate } from "../../types";
import type { TimelineRowItem } from "../timeline/TimelineGrid";
import { LOGIC_GATE_H, LOGIC_GATE_W } from "../canvas/LogicGate";
import { MAX_COL, MAX_ROW } from "../canvas/useBoardColumns";
import {
  CARD_GAP_X,
  CARD_GAP_Y,
  CARD_H,
  CARD_W,
  COL_W,
  MAX_ZOOM,
  MIN_ZOOM,
  ROW_H,
  dayKey,
  type Col,
  type Rect,
} from "../canvas/geometry";

export const RULER_W = 56;
const GRAPH_MARGIN_COLS = 2;
const GRAPH_MARGIN_ROWS = 1;
const GRAPH_PAN_BUFFER_COLS = 8;
const GRAPH_PAN_BUFFER_ROWS = 8;
export const GRAPH_PAN_BUFFER_X = GRAPH_PAN_BUFFER_COLS * COL_W;
export const GRAPH_PAN_BUFFER_Y = GRAPH_PAN_BUFFER_ROWS * ROW_H;

export interface BoardMetrics {
  boardW: number;
  graphOffsetX: number;
  graphCanvasH: number;
  graphOffsetY: number;
}

/** The drawing surface both panes scroll, and the graph's origin inside it. */
export function boardMetrics({
  isGraph,
  maxCol,
  maxRow,
  timelineColumnCount,
  timelineCellWidth,
  boardViewport,
  zoom,
}: {
  isGraph: boolean;
  maxCol: number;
  maxRow: number;
  timelineColumnCount: number;
  timelineCellWidth: number;
  boardViewport: { width: number; height: number };
  zoom: number;
}): BoardMetrics {
  const graphContentW = Math.max(maxCol + 1, 1) * COL_W;
  const graphMinimumW =
    graphContentW +
    (GRAPH_MARGIN_COLS * COL_W + GRAPH_PAN_BUFFER_X) * 2;
  const boardW = isGraph
    ? Math.max(
        graphMinimumW,
        boardViewport.width / zoom + GRAPH_PAN_BUFFER_X * 2,
      )
    : Math.max(timelineColumnCount, 1) * timelineCellWidth + RULER_W;
  // Content narrower than the viewport is drawn centred in it: a fresh
  // project's single node then sits in the middle of the canvas with room on
  // every side, instead of pinned to the top-left corner.
  const graphSlackX = Math.max(
    0,
    boardViewport.width / zoom - graphContentW - GRAPH_MARGIN_COLS * COL_W * 2,
  );
  const graphOffsetX = isGraph
    ? GRAPH_PAN_BUFFER_X + GRAPH_MARGIN_COLS * COL_W + graphSlackX / 2
    : 0;

  const graphContentH = Math.max(maxRow + 1, 1) * ROW_H;
  const graphMinimumH =
    graphContentH +
    (GRAPH_MARGIN_ROWS * ROW_H + GRAPH_PAN_BUFFER_Y) * 2;
  const graphCanvasH = Math.max(
    graphMinimumH,
    boardViewport.height / zoom + GRAPH_PAN_BUFFER_Y * 2,
  );
  const graphSlackY = Math.max(
    0,
    boardViewport.height / zoom - graphContentH - GRAPH_MARGIN_ROWS * ROW_H * 2,
  );
  const graphOffsetY = isGraph
    ? GRAPH_PAN_BUFFER_Y + GRAPH_MARGIN_ROWS * ROW_H + graphSlackY / 2
    : 0;

  return { boardW, graphOffsetX, graphCanvasH, graphOffsetY };
}

/** Room the opening scroll leaves between the view's edge and the first card. */
const OPENING_PADDING = 72;

/**
 * Where a board this session has not shown opens. With no content it is the
 * buffer origin, where logical (0,0) is. With content — cards, groups and
 * annotations — it frames it: centred
 * along an axis they fit in, else starting, with a little room, at the card
 * nearest their top-left corner, which is then always in view. Saved
 * positions need not start at the origin, and opening on the origin showed
 * nothing but margin when they did not.
 *
 * `cards` are the content's rectangles, in board pixels before the zoom.
 */
export function openingScroll(
  cards: readonly Rect[],
  zoom: number,
  viewport: { width: number; height: number },
): { left: number; top: number } {
  if (cards.length === 0) {
    return { left: GRAPH_PAN_BUFFER_X * zoom, top: GRAPH_PAN_BUFFER_Y * zoom };
  }
  const bounds = {
    left: Math.min(...cards.map((card) => card.left)),
    right: Math.max(...cards.map((card) => card.right)),
    top: Math.min(...cards.map((card) => card.top)),
    bottom: Math.max(...cards.map((card) => card.bottom)),
  };
  let anchor = cards[0];
  let nearest = Infinity;
  for (const card of cards) {
    const distance = Math.hypot(card.left - bounds.left, card.top - bounds.top);
    if (distance < nearest) {
      nearest = distance;
      anchor = card;
    }
  }
  // Content that fits is centred even when that leaves less than the usual
  // room around it: a four-quadrant template 20px narrower than the view
  // otherwise opened from its left edge with its right-hand quadrants cut.
  const axis = (start: number, end: number, from: number, size: number) => {
    const padding = Math.min(OPENING_PADDING, size / 8);
    const scroll =
      (end - start) * zoom <= size
        ? ((start + end) / 2) * zoom - size / 2
        : from * zoom - padding;
    return Math.max(0, scroll);
  };
  return {
    left: axis(bounds.left, bounds.right, anchor.left, viewport.width),
    top: axis(bounds.top, bounds.bottom, anchor.top, viewport.height),
  };
}

/**
 * Where the timeline opens. Its range starts two weeks before today (further
 * back for older records), so scroll 0 is a fortnight of empty days. It opens
 * on today instead — or, when nothing recorded or planned is in view from
 * there, on the first such day after today, else on the latest one before it.
 *
 * Positions run along the date axis from where the days start: the lane
 * headers above (vertical) or beside (horizontal) them are sticky, so a day
 * scrolled to offset 0 sits just past them. `daySize` is a day's length along
 * that axis and `gapSize` a collapsed run of empty days'; both before the zoom.
 */
export function timelineOpeningScroll({
  days,
  rowItems,
  todayKey,
  contentDays,
  orientation,
  daySize,
  gapSize,
  viewport,
  zoom,
}: {
  days: readonly Date[];
  rowItems: readonly TimelineRowItem[];
  todayKey: string;
  /** Days that carry a record or a plan. */
  contentDays: ReadonlySet<string>;
  orientation: "vertical" | "horizontal";
  daySize: number;
  gapSize: number;
  viewport: { width: number; height: number };
  zoom: number;
}): { left: number; top: number } {
  // Every day's start, a collapsed one at its gap's; the same walk as dayY.
  const starts: number[] = new Array(days.length).fill(0);
  let position = 0;
  let next = 0;
  for (const item of rowItems) {
    if (item.kind === "day") {
      starts[item.dayIndex] = position;
      next = item.dayIndex + 1;
      position += daySize;
    } else {
      for (let k = 0; k < item.count; k++) starts[next + k] = position;
      next += item.count;
      position += gapSize;
    }
  }
  const keys = days.map(dayKey);
  const todayIndex = keys.indexOf(todayKey);
  let target = todayIndex >= 0 ? starts[todayIndex] : 0;
  const marked = keys.flatMap((key, index) => (contentDays.has(key) ? [starts[index]] : []));
  const span = (orientation === "vertical" ? viewport.height : viewport.width) / zoom;
  const inView = (start: number) => start >= target && start + daySize <= target + span;
  if (marked.length > 0 && !marked.some(inView)) {
    target = marked.find((start) => start > target) ?? marked[marked.length - 1];
  }
  const scroll = Math.max(0, target * zoom);
  return orientation === "vertical" ? { left: 0, top: scroll } : { left: scroll, top: 0 };
}

/** The grid cell a board-space point falls in, clamped to the legal range. */
export function graphCellAt(
  contentX: number,
  contentY: number,
): { col: number; row: number } {
  return {
    col: Math.max(0, Math.min(MAX_COL, Math.floor(contentX / COL_W))),
    row: Math.max(0, Math.min(MAX_ROW, Math.floor(contentY / ROW_H))),
  };
}

/**
 * The free grid cell nearest to anchor for a new card of the default size:
 * the anchor itself when nothing overlaps it, else the closest cell ring by
 * ring (right, below, left, above first). Without it every card made from the
 * toolbar landed on the middle of the view, exactly on top of the last one.
 */
export function nearestFreeGraphCell(
  anchor: { col: number; row: number },
  cols: readonly Col[],
): { col: number; row: number } {
  const overlaps = (col: number, row: number) =>
    cols.some(
      (card) =>
        Math.abs((col - card.index) * COL_W) < (CARD_W + card.width) / 2 + CARD_GAP_X &&
        Math.abs((row - card.row) * ROW_H) < (CARD_H + card.height) / 2 + CARD_GAP_Y,
    );
  const inBoard = (col: number, row: number) =>
    col >= 0 && col <= MAX_COL && row >= 0 && row <= MAX_ROW;
  const maxRing = 24;
  for (let ring = 0; ring <= maxRing; ring++) {
    const candidates: { col: number; row: number }[] = [];
    for (let dc = -ring; dc <= ring; dc++) {
      for (let dr = -ring; dr <= ring; dr++) {
        if (Math.max(Math.abs(dc), Math.abs(dr)) !== ring) continue;
        candidates.push({ col: anchor.col + dc, row: anchor.row + dr });
      }
    }
    // Nearest first by grid distance; the four direct neighbours win ties,
    // right and below before left and above.
    const order = (c: { col: number; row: number }) => {
      const dc = c.col - anchor.col;
      const dr = c.row - anchor.row;
      const manhattan = Math.abs(dc) + Math.abs(dr);
      const side = dc > 0 ? 0 : dr > 0 ? 1 : dc < 0 ? 2 : 3;
      return manhattan * 4 + side;
    };
    candidates.sort((a, b) => order(a) - order(b));
    for (const candidate of candidates) {
      if (inBoard(candidate.col, candidate.row) && !overlaps(candidate.col, candidate.row)) {
        return candidate;
      }
    }
  }
  return anchor;
}

/** Everything a wire has to route around: the cards and the logic gates. */
export function graphObstacleRects(
  cols: Col[],
  logicGates: LogicGate[],
  centerX: (column: Col) => number,
  rowTop: (row: number) => number,
  graphOffsetX: number,
  graphOffsetY: number,
): Rect[] {
  return [
    ...cols.map((col) => ({
      left: centerX(col) - col.width / 2,
      right: centerX(col) + col.width / 2,
      top: rowTop(col.row) + ROW_H / 2 - col.height / 2,
      bottom: rowTop(col.row) + ROW_H / 2 + col.height / 2,
    })),
    ...logicGates.map((gate) => ({
      left: graphOffsetX + gate.x - LOGIC_GATE_W / 2,
      right: graphOffsetX + gate.x + LOGIC_GATE_W / 2,
      top: graphOffsetY + gate.y - LOGIC_GATE_H / 2,
      bottom: graphOffsetY + gate.y + LOGIC_GATE_H / 2,
    })),
  ];
}

/**
 * The zoom and the board-space point that bring the given cards fully into a
 * viewport of this size. Null when there is nothing to fit.
 */
export function fitViewToCards(
  candidates: Col[],
  centerX: (column: Col) => number,
  rowTop: (row: number) => number,
  viewport: { width: number; height: number },
): { zoom: number; focusX: number; focusY: number } | null {
  if (!candidates.length) return null;
  const left = Math.min(...candidates.map((column) => centerX(column) - column.width / 2));
  const right = Math.max(...candidates.map((column) => centerX(column) + column.width / 2));
  const top = Math.min(
    ...candidates.map((column) => rowTop(column.row) + ROW_H / 2 - column.height / 2),
  );
  const bottom = Math.max(
    ...candidates.map((column) => rowTop(column.row) + ROW_H / 2 + column.height / 2),
  );
  const padding = 72;
  const nextZoom = Math.max(
    MIN_ZOOM,
    Math.min(
      MAX_ZOOM,
      Math.floor(
        Math.min(
          viewport.width / Math.max(1, right - left + padding * 2),
          viewport.height / Math.max(1, bottom - top + padding * 2),
        ) * 10,
      ) / 10,
    ),
  );
  return { zoom: nextZoom, focusX: (left + right) / 2, focusY: (top + bottom) / 2 };
}
