import { describe, expect, it } from "vitest";

import { corridorPath, layoutDungeon, packGrid, SPACING } from "./dungeonLayout";

describe("packGrid", () => {
  it("snaps dragged (fractional) positions to tiles and never stacks two rooms", () => {
    const packed = packGrid(
      new Map([
        ["a", { col: 1.4, row: 0 }],
        ["b", { col: 1.6, row: 0.2 }],
        ["c", { col: 7, row: 3 }],
      ]),
    );
    const at = (id: string) => `${packed.get(id)!.col}:${packed.get(id)!.row}`;
    expect(new Set([at("a"), at("b"), at("c")]).size).toBe(3);
  });

  it("drops empty columns and rows", () => {
    const packed = packGrid(
      new Map([
        ["a", { col: 0, row: 0 }],
        ["b", { col: 40, row: 9 }],
      ]),
    );
    expect(packed.get("b")).toEqual({ col: 1, row: 1 });
  });
});

describe("corridorPath", () => {
  it("walks across then down, without the ends", () => {
    expect(corridorPath({ col: 0, row: 0 }, { col: 2, row: 1 })).toEqual([
      { col: 1, row: 0 },
      { col: 2, row: 0 },
    ]);
  });
});

describe("layoutDungeon", () => {
  const colOf = new Map([
    ["schema", { index: 0, row: 0 }],
    ["api", { index: 0, row: 1 }],
    ["login", { index: 0, row: 2 }],
    ["docs", { index: 1, row: 2 }],
  ]);

  it("spreads rooms out, puts the boss past the deepest row, and joins them by corridors", () => {
    const layout = layoutDungeon(["schema", "api", "login", "docs", "orphan"], colOf, [
      { from: "schema", to: "api" },
      { from: "api", to: "login" },
      { from: "api", to: "docs" },
    ]);
    expect(layout.rooms.get("api")).toEqual({ col: 0, row: SPACING });
    // A node the board did not place gets a column of its own.
    expect(layout.rooms.get("orphan")!.col).toBeGreaterThan(layout.rooms.get("docs")!.col);
    const deepest = Math.max(...[...layout.rooms.values()].map((p) => p.row));
    expect(layout.boss.row).toBeGreaterThan(deepest);
    // No corridor tile sits under a room.
    const rooms = new Set([...layout.rooms.values()].map((p) => `${p.col}:${p.row}`));
    expect(layout.corridors.some((p) => rooms.has(`${p.col}:${p.row}`))).toBe(false);
    expect(layout.corridors.length).toBeGreaterThan(0);
  });
});
