import { describe, expect, it } from "vitest";
import {
  DEFAULT_TILE,
  gridSlots,
  isoBounds,
  isoDepth,
  isoProject,
  isoUnproject,
  placeOnGrid,
  roomOrigins,
} from "./layout";

describe("isometric projection", () => {
  it("projects 2:1 dimetric", () => {
    expect(DEFAULT_TILE).toEqual({ w: 64, h: 32 });
    expect(isoProject(0, 0)).toEqual({ x: 0, y: 0 });
    expect(isoProject(1, 0)).toEqual({ x: 32, y: 16 });
    expect(isoProject(0, 1)).toEqual({ x: -32, y: 16 });
    expect(isoProject(2, 3, { w: 100, h: 50 })).toEqual({ x: -50, y: 125 });
  });

  it("unprojects back to the tile", () => {
    for (const [col, row] of [
      [0, 0],
      [3, 1],
      [1, 4],
      [7, 7],
    ]) {
      const { x, y } = isoProject(col, row);
      expect(isoUnproject(x, y)).toEqual({ col, row });
      // A few pixels off still lands on the same tile.
      expect(isoUnproject(x + 3, y + 2)).toEqual({ col, row });
    }
  });

  it("draws further down-right in front", () => {
    expect(isoDepth(2, 3)).toBe(5);
    expect(isoDepth(0, 0)).toBeLessThan(isoDepth(1, 0));
  });

  it("bounds the tiles' diamonds", () => {
    expect(isoBounds([])).toEqual({ minX: 0, minY: 0, maxX: 0, maxY: 0, width: 0, height: 0 });
    expect(isoBounds([{ col: 0, row: 0 }])).toEqual({ minX: -32, minY: 0, maxX: 32, maxY: 32, width: 64, height: 32 });
    const box = isoBounds([
      { col: 0, row: 0 },
      { col: 2, row: 0 },
      { col: 0, row: 2 },
    ]);
    expect(box).toMatchObject({ minX: -96, maxX: 96, minY: 0, maxY: 64 });
  });
});

describe("grid slots", () => {
  it("fills row by row", () => {
    expect(gridSlots(5, 2)).toEqual([
      { col: 0, row: 0 },
      { col: 1, row: 0 },
      { col: 0, row: 1 },
      { col: 1, row: 1 },
      { col: 0, row: 2 },
    ]);
    expect(gridSlots(0, 3)).toEqual([]);
    expect(gridSlots(2, 0)).toEqual([
      { col: 0, row: 0 },
      { col: 0, row: 1 },
    ]);
  });

  it("spaces rooms by their size plus a corridor", () => {
    expect(roomOrigins(3, 2, 4)).toEqual([
      { col: 0, row: 0 },
      { col: 5, row: 0 },
      { col: 0, row: 5 },
    ]);
    expect(roomOrigins(2, 2, 3, 0)[1]).toEqual({ col: 3, row: 0 });
  });

  it("takes the board's columns, and puts what they lack after the last", () => {
    const colOf = new Map([
      ["a", { index: 0, row: 0 }],
      ["b", { index: 1, row: 2 }],
    ]);
    const placed = placeOnGrid(["a", "x", "b", "y"], colOf);
    expect([...placed.entries()]).toEqual([
      ["a", { col: 0, row: 0 }],
      ["b", { col: 1, row: 2 }],
      ["x", { col: 2, row: 0 }],
      ["y", { col: 2, row: 1 }],
    ]);
    expect([...placeOnGrid(["z"], new Map()).values()]).toEqual([{ col: 0, row: 0 }]);
  });
});
