// Grid to screen for the isometric skins. The world is laid out on a flat
// grid of (col, row) tiles; a 2:1 dimetric projection turns each tile into a
// screen position, the classic 30°×45° look of Kenney's isometric packs.
// Depth (draw order) is col + row: a tile further down-right is in front.
//
// The boss map takes its grid from the canvas's own board columns
// (components/canvas/useBoardColumns: dependency depth plus the positions
// people dragged), so the dungeon reads like the board; this module only
// projects it.

export interface Tile {
  /** Screen width of one tile's diamond. */
  w: number;
  /** Screen height of one tile's diamond: w / 2 for 2:1 dimetric. */
  h: number;
}

export const DEFAULT_TILE: Tile = { w: 64, h: 32 };

export interface GridPoint {
  col: number;
  row: number;
}

export interface ScreenPoint {
  x: number;
  y: number;
}

/** The screen position of a tile's top corner. */
export function isoProject(col: number, row: number, tile: Tile = DEFAULT_TILE): ScreenPoint {
  return { x: ((col - row) * tile.w) / 2, y: ((col + row) * tile.h) / 2 };
}

/** The inverse of isoProject, to the nearest tile (hit-testing a pointer). */
export function isoUnproject(x: number, y: number, tile: Tile = DEFAULT_TILE): GridPoint {
  const a = (2 * x) / tile.w;
  const b = (2 * y) / tile.h;
  return { col: Math.round((a + b) / 2), row: Math.round((b - a) / 2) };
}

/** Draw order: later (higher) is in front. */
export function isoDepth(col: number, row: number): number {
  return col + row;
}

export interface Bounds {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
  width: number;
  height: number;
}

/** The screen box holding every tile's diamond, for sizing the scene. An
 * empty set is a zero box at the origin. */
export function isoBounds(points: GridPoint[], tile: Tile = DEFAULT_TILE): Bounds {
  if (!points.length) return { minX: 0, minY: 0, maxX: 0, maxY: 0, width: 0, height: 0 };
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const point of points) {
    const { x, y } = isoProject(point.col, point.row, tile);
    // The diamond spans x ± w/2 and y … y + h from its top corner.
    minX = Math.min(minX, x - tile.w / 2);
    maxX = Math.max(maxX, x + tile.w / 2);
    minY = Math.min(minY, y);
    maxY = Math.max(maxY, y + tile.h);
  }
  return { minX, minY, maxX, maxY, width: maxX - minX, height: maxY - minY };
}

/** n slots filled row by row, `perRow` to a row: desks in a room, rooms on
 * a floor. */
export function gridSlots(n: number, perRow: number): GridPoint[] {
  const width = Math.max(1, Math.floor(perRow));
  return Array.from({ length: Math.max(0, n) }, (_, i) => ({
    col: i % width,
    row: Math.floor(i / width),
  }));
}

/** Room origins on the floor: each room `size` tiles square plus a `gap`
 * corridor, rooms laid out `perRow` to a row. */
export function roomOrigins(count: number, perRow: number, size: number, gap = 1): GridPoint[] {
  const step = size + gap;
  return gridSlots(count, perRow).map((slot) => ({ col: slot.col * step, row: slot.row * step }));
}

/** The grid position of each id the column map knows: (index, row) of the
 * board's dependency columns becomes (col, row) of the dungeon. Ids it does
 * not know go in a column of their own after the last. */
export function placeOnGrid(
  ids: string[],
  colOf: ReadonlyMap<string, { index: number; row: number }>,
): Map<string, GridPoint> {
  const placed = new Map<string, GridPoint>();
  let lastCol = -1;
  for (const id of ids) {
    const at = colOf.get(id);
    if (!at) continue;
    placed.set(id, { col: at.index, row: at.row });
    lastCol = Math.max(lastCol, at.index);
  }
  let row = 0;
  for (const id of ids) {
    if (placed.has(id)) continue;
    placed.set(id, { col: lastCol + 1, row: row++ });
  }
  return placed;
}
