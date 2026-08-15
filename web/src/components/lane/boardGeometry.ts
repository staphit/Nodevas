/**
 * Board sizing maths [B-06].
 *
 * How large the scrolling surface is, where logical (0,0) lands inside it, and
 * which grid cell a pointer is over. All of it is a pure function of the grid,
 * the viewport and the zoom, so none of it needs React.
 */

import type { LogicGate } from "../../types";
import { LOGIC_GATE_H, LOGIC_GATE_W } from "../canvas/LogicGate";
import { MAX_COL, MAX_ROW } from "../canvas/useBoardColumns";
import {
  COL_W,
  MAX_ZOOM,
  MIN_ZOOM,
  ROW_H,
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
