// Where the boss map puts things, in grid units. The grid comes from the
// board's own columns (useBoardColumns: dependency depth as the row, a slot
// in that depth as the index, positions people dragged kept), so the map
// reads like the board. Those positions are continuous and sparse, so they
// are snapped to whole tiles, packed (empty columns and rows dropped), and
// spread out by SPACING to leave a corridor tile between rooms.

import { placeOnGrid, type GridPoint } from "../layout";

/** Tiles from one room to the next: a room, then two corridor tiles. */
export const SPACING = 3;

/** Snaps to whole tiles, drops empty columns and rows, and moves a room that
 * lands on an occupied tile down its column until it is free. */
export function packGrid(placed: ReadonlyMap<string, GridPoint>): Map<string, GridPoint> {
  const snapped = [...placed.entries()].map(([id, p]) => ({
    id,
    col: Math.round(p.col),
    row: Math.round(p.row),
    order: p.col * 1e6 + p.row,
  }));
  const colRank = new Map([...new Set(snapped.map((p) => p.col))].sort((a, b) => a - b).map((c, i) => [c, i]));
  const rowRank = new Map([...new Set(snapped.map((p) => p.row))].sort((a, b) => a - b).map((r, i) => [r, i]));
  snapped.sort((a, b) => a.order - b.order || a.id.localeCompare(b.id));
  const taken = new Set<string>();
  const out = new Map<string, GridPoint>();
  for (const p of snapped) {
    const col = colRank.get(p.col)!;
    let row = rowRank.get(p.row)!;
    while (taken.has(`${col}:${row}`)) row += 1;
    taken.add(`${col}:${row}`);
    out.set(p.id, { col, row });
  }
  return out;
}

export interface DungeonLayout {
  /** Monster rooms, by node id, in tiles. */
  rooms: Map<string, GridPoint>;
  /** The boss arena's first tile (it is 2×2). */
  boss: GridPoint;
  /** Corridor tiles (not under a room or the arena). */
  corridors: GridPoint[];
  /** Every tile drawn, for sizing the scene. */
  extent: GridPoint[];
}

/** One step at a time from a to b: across the columns first, then down the
 * rows, every tile on the way except the ends. */
export function corridorPath(a: GridPoint, b: GridPoint): GridPoint[] {
  const path: GridPoint[] = [];
  let { col, row } = a;
  while (col !== b.col) {
    col += Math.sign(b.col - col);
    path.push({ col, row });
  }
  while (row !== b.row) {
    row += Math.sign(b.row - row);
    path.push({ col, row });
  }
  path.pop();
  return path;
}

export interface LayoutEdge {
  from: string;
  to: string;
}

export function layoutDungeon(
  ids: string[],
  colOf: ReadonlyMap<string, { index: number; row: number }>,
  edges: LayoutEdge[],
): DungeonLayout {
  // The board's (index, row) becomes the map's (col, row): index across,
  // dependency depth down.
  const packed = packGrid(placeOnGrid(ids, colOf));
  const rooms = new Map<string, GridPoint>();
  let maxCol = 0;
  let maxRow = 0;
  for (const [id, p] of packed) {
    const at = { col: p.col * SPACING, row: p.row * SPACING };
    rooms.set(id, at);
    maxCol = Math.max(maxCol, at.col);
    maxRow = Math.max(maxRow, at.row);
  }
  // The boss waits past the deepest row, in the middle.
  const boss = {
    col: Math.max(0, Math.round(maxCol / 2 / SPACING) * SPACING),
    // Two rooms' depth past the last row: the boss is tall and must not hide it.
    row: rooms.size ? maxRow + 2 * SPACING : 0,
  };

  const occupied = new Set<string>();
  const key = (p: GridPoint) => `${p.col}:${p.row}`;
  for (const p of rooms.values()) occupied.add(key(p));
  for (let dc = 0; dc < 2; dc++) for (let dr = 0; dr < 2; dr++) occupied.add(key({ col: boss.col + dc, row: boss.row + dr }));

  const corridors = new Map<string, GridPoint>();
  const add = (p: GridPoint) => {
    if (!occupied.has(key(p))) corridors.set(key(p), p);
  };
  const hasOut = new Set<string>();
  for (const edge of edges) {
    const a = rooms.get(edge.from);
    const b = rooms.get(edge.to);
    if (!a || !b) continue;
    hasOut.add(edge.from);
    corridorPath(a, b).forEach(add);
  }
  // Rooms nothing depends on lead to the boss.
  for (const [id, p] of rooms) {
    if (!hasOut.has(id)) corridorPath(p, boss).forEach(add);
  }

  const extent = [
    ...rooms.values(),
    ...corridors.values(),
    boss,
    { col: boss.col + 1, row: boss.row + 1 },
  ];
  return { rooms, boss, corridors: [...corridors.values()], extent };
}
