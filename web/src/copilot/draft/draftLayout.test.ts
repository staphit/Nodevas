import { describe, expect, it } from "vitest";
import { cardCells, draftDepths, layoutDraft, layoutExtent, LAYOUT_MAX_COL, LAYOUT_MAX_ROW } from "./draftLayout";
import type { Draft } from "./draftStore";

const diamond: Draft = {
  summary: "",
  nodes: [
    { key: "a", title: "A", kind: "task" },
    { key: "b", title: "B", kind: "task", after: ["a"] },
    { key: "c", title: "C", kind: "task", after: ["a"] },
    { key: "d", title: "D", kind: "task", after: ["b", "c"] },
  ],
};

const cells = (entries: Record<string, [number, number]>) =>
  new Map(Object.entries(entries).map(([id, [col, row]]) => [id, [{ col, row }]]));

const overlaps = (layout: Record<string, { col: number; row: number }>, occupied: Map<string, { col: number; row: number }[]>) => {
  const taken = new Set([...occupied.values()].flat().map((cell) => `${cell.col},${cell.row}`));
  return Object.values(layout).some((cell) => taken.has(`${cell.col},${cell.row}`));
};

describe("draftLayout", () => {
  it("puts each dependency depth in its own column", () => {
    expect(Object.fromEntries(draftDepths(diamond))).toEqual({ a: 0, b: 1, c: 1, d: 2 });
    const layout = layoutDraft(diamond, { occupied: new Map() });
    expect(layout).toEqual({
      a: { col: 0, row: 0 },
      b: { col: 1, row: 0 },
      c: { col: 1, row: 1 },
      d: { col: 2, row: 0 },
    });
  });

  it("survives a cycle", () => {
    const looped: Draft = {
      summary: "",
      nodes: [
        { key: "a", title: "A", kind: "task", after: ["b"] },
        { key: "b", title: "B", kind: "task", after: ["a"] },
      ],
    };
    const layout = layoutDraft(looped, { occupied: new Map() });
    expect(Object.keys(layout).sort()).toEqual(["a", "b"]);
  });

  it("goes right of the board's content, clear of every card", () => {
    const occupied = cells({ x: [0, 0], y: [1, 0], z: [3, 2] });
    const layout = layoutDraft(diamond, { occupied });
    expect(overlaps(layout, occupied)).toBe(false);
    // One empty column after the rightmost card.
    expect(layout.a).toEqual({ col: 5, row: 0 });
  });

  it("goes right of the selection, and below what is already there", () => {
    const occupied = cells({ s: [2, 3], other: [3, 3], more: [4, 4] });
    const layout = layoutDraft(diamond, { occupied, selection: ["s"] });
    expect(overlaps(layout, occupied)).toBe(false);
    expect(layout.a.col).toBe(3);
    expect(layout.a.row).toBeGreaterThanOrEqual(3);
  });

  it("goes right of an existing node the draft waits for when nothing is selected", () => {
    const occupied = cells({ "node-1": [10, 6], far: [40, 0] });
    const draft: Draft = { summary: "", nodes: [{ key: "a", title: "A", kind: "task", after: ["existing:node-1"] }] };
    expect(layoutDraft(draft, { occupied })).toEqual({ a: { col: 11, row: 6 } });
  });

  it("stays inside the board's bounds", () => {
    const occupied = cells({ edge: [LAYOUT_MAX_COL, LAYOUT_MAX_ROW] });
    const layout = layoutDraft(diamond, { occupied, selection: ["edge"] });
    for (const cell of Object.values(layout)) {
      expect(cell.col).toBeGreaterThanOrEqual(0);
      expect(cell.col).toBeLessThanOrEqual(LAYOUT_MAX_COL);
      expect(cell.row).toBeGreaterThanOrEqual(0);
      expect(cell.row).toBeLessThanOrEqual(LAYOUT_MAX_ROW);
      expect(Number.isInteger(cell.col) && Number.isInteger(cell.row)).toBe(true);
    }
    expect(overlaps(layout, occupied)).toBe(false);
    expect(layoutExtent(layout)).not.toBeNull();
  });

  it("counts a wide card's neighbouring cells as taken", () => {
    expect(cardCells({ index: 4, row: 1, width: 360, height: 68 }).map((cell) => cell.col)).toEqual([3, 4, 5]);
    expect(cardCells({ index: 4, row: 1, width: 152, height: 68 })).toEqual([{ col: 4, row: 1 }]);
  });
});
