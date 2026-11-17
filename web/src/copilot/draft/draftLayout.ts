/**
 * Where a draft's nodes go on the board, in grid cells.
 *
 * The same cells graph.ui.positions stores (column 0..499, row 0..200,
 * useBoardColumns.ts): the ghosts are drawn at them and the Create sends them,
 * so the nodes land exactly where they were previewed. A plain layered layout
 * by dependency depth, one column per layer, placed right of the selected
 * nodes (or of the existing nodes the draft waits for) when there are any,
 * otherwise in the first empty area right of the board's content, and never
 * on a cell a card already takes.
 */

import { CARD_H, CARD_W, COL_W, ROW_H } from "../../components/canvas/geometry";
import { existingIdOf, type Draft } from "./draftStore";

export const LAYOUT_MAX_COL = 499;
export const LAYOUT_MAX_ROW = 200;

export interface Cell {
  col: number;
  row: number;
}

export interface LayoutOptions {
  /** Cells the board's cards take, by node id (wide cards may list several). */
  occupied: ReadonlyMap<string, Cell[]> | Record<string, Cell[]>;
  /** The canvas selection, by node id; the draft goes right of it. */
  selection?: readonly string[];
  /**
   * Without a selection, go right of the existing nodes the draft waits for
   * (default true). The board's overlay fixes its anchor when the draft
   * arrives instead, so linking a ghost to a card does not move the draft.
   */
  followRefs?: boolean;
}

/** Depth of each node: 0 for one that waits for no other draft node. */
export function draftDepths(draft: Draft): Map<string, number> {
  const byKey = new Map(draft.nodes.map((node) => [node.key, node]));
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const depthOf = (key: string): number => {
    const known = depth.get(key);
    if (known !== undefined) return known;
    // A cycle (the card says so): break it here rather than loop.
    if (visiting.has(key)) return 0;
    visiting.add(key);
    let value = 0;
    for (const ref of byKey.get(key)?.after ?? []) {
      if (byKey.has(ref) && ref !== key) value = Math.max(value, depthOf(ref) + 1);
    }
    visiting.delete(key);
    depth.set(key, value);
    return value;
  };
  for (const node of draft.nodes) depthOf(node.key);
  return depth;
}

function cellsOf(occupied: LayoutOptions["occupied"], id: string): Cell[] {
  return occupied instanceof Map ? (occupied.get(id) ?? []) : ((occupied as Record<string, Cell[]>)[id] ?? []);
}

function allCells(occupied: LayoutOptions["occupied"]): Cell[] {
  return occupied instanceof Map
    ? [...occupied.values()].flat()
    : Object.values(occupied as Record<string, Cell[]>).flat();
}

const cellKey = (col: number, row: number) => `${col},${row}`;

/** The existing nodes a draft waits for, by id. */
export function waitedForIds(draft: Draft): string[] {
  return [
    ...new Set(draft.nodes.flatMap((node) => (node.after ?? []).map(existingIdOf).filter(Boolean))),
  ];
}

/** Grid cells for every draft node, by key. */
export function layoutDraft(draft: Draft, options: LayoutOptions): Record<string, Cell> {
  if (draft.nodes.length === 0) return {};
  const depths = draftDepths(draft);
  const layers: string[][] = [];
  for (const node of draft.nodes) {
    const layer = depths.get(node.key) ?? 0;
    (layers[layer] ??= []).push(node.key);
  }
  const compact = layers.filter((layer) => layer && layer.length > 0);
  const width = Math.min(compact.length, LAYOUT_MAX_COL + 1);
  const height = Math.min(Math.max(...compact.map((layer) => layer.length)), LAYOUT_MAX_ROW + 1);

  const taken = new Set(allCells(options.occupied).map((cell) => cellKey(cell.col, cell.row)));
  const content = allCells(options.occupied);

  // Anchors: the selection, else the existing nodes the draft waits for.
  let anchors = (options.selection ?? []).flatMap((id) => cellsOf(options.occupied, id));
  if (anchors.length === 0 && options.followRefs !== false) {
    anchors = waitedForIds(draft).flatMap((id) => cellsOf(options.occupied, id));
  }

  let originCol: number;
  let originRow: number;
  if (anchors.length > 0) {
    originCol = Math.max(...anchors.map((cell) => cell.col)) + 1;
    originRow = Math.min(...anchors.map((cell) => cell.row));
  } else if (content.length > 0) {
    // One empty column between the content and the draft.
    originCol = Math.max(...content.map((cell) => cell.col)) + 2;
    originRow = Math.min(...content.map((cell) => cell.row));
  } else {
    originCol = 0;
    originRow = 0;
  }

  const lastCol = LAYOUT_MAX_COL - width + 1;
  const lastRow = LAYOUT_MAX_ROW - height + 1;
  const free = (col: number, row: number) => {
    for (let dc = 0; dc < width; dc++) {
      for (let dr = 0; dr < height; dr++) {
        if (taken.has(cellKey(col + dc, row + dr))) return false;
      }
    }
    return true;
  };
  const clamp = (value: number, max: number) => Math.max(0, Math.min(max, value));
  const startCol = clamp(originCol, lastCol);
  const startRow = clamp(originRow, lastRow);

  // Right of the anchor first, then below it, then further right.
  let spot: Cell | null = null;
  search: for (let col = startCol; col <= lastCol; col++) {
    for (let row = startRow; row <= lastRow; row++) {
      if (free(col, row)) {
        spot = { col, row };
        break search;
      }
    }
  }
  if (!spot) {
    // Nothing right of it fits: anywhere on the board.
    search: for (let col = 0; col <= lastCol; col++) {
      for (let row = 0; row <= lastRow; row++) {
        if (free(col, row)) {
          spot = { col, row };
          break search;
        }
      }
    }
  }
  const origin = spot ?? { col: startCol, row: startRow };

  const cells: Record<string, Cell> = {};
  compact.forEach((layer, layerIndex) => {
    layer.forEach((key, rowIndex) => {
      cells[key] = {
        col: clamp(origin.col + layerIndex, LAYOUT_MAX_COL),
        row: clamp(origin.row + rowIndex, LAYOUT_MAX_ROW),
      };
    });
  });
  return cells;
}

/**
 * The cells a laid-out card covers: its own, and the neighbours a wide or
 * tall card reaches into (cards are centred in their cell). Positions can be
 * fractional after a free drag, so both ends are rounded outwards.
 */
export function cardCells(column: {
  index: number;
  row: number;
  width: number;
  height: number;
}): Cell[] {
  const spanX = Math.max(0, (column.width - CARD_W) / 2 / COL_W);
  const spanY = Math.max(0, (column.height - CARD_H) / 2 / ROW_H);
  const cells: Cell[] = [];
  for (let col = Math.floor(column.index - spanX); col <= Math.ceil(column.index + spanX); col++) {
    for (let row = Math.floor(column.row - spanY); row <= Math.ceil(column.row + spanY); row++) {
      if (col >= 0 && row >= 0) cells.push({ col, row });
    }
  }
  return cells;
}

/** The board's cards as LayoutOptions.occupied. */
export function occupiedOf(
  columns: Iterable<{ node: { id: string }; index: number; row: number; width: number; height: number }>,
): Map<string, Cell[]> {
  const map = new Map<string, Cell[]>();
  for (const column of columns) map.set(column.node.id, cardCells(column));
  return map;
}

/** The furthest cell a layout reaches, so the board can grow to show it. */
export function layoutExtent(cells: Record<string, Cell>): Cell | null {
  const values = Object.values(cells);
  if (values.length === 0) return null;
  return {
    col: Math.max(...values.map((cell) => cell.col)),
    row: Math.max(...values.map((cell) => cell.row)),
  };
}
