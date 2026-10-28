import { describe, expect, it } from "vitest";
import { CARD_H, CARD_W, type Col } from "../canvas/geometry";
import type { GraphNode } from "../../types";
import { nearestFreeGraphCell } from "./boardGeometry";

function card(id: string, index: number, row: number): Col {
  return { node: { id, title: id } as GraphNode, index, row, width: CARD_W, height: CARD_H };
}

// "新增節點" from the toolbar anchors at the middle of the view. Pressing it
// five times without touching the board put five cards on exactly the same
// cell, each hiding the one before: the board looked as if four of them had
// never been made.
describe("nearestFreeGraphCell", () => {
  it("keeps the anchor when nothing is there", () => {
    expect(nearestFreeGraphCell({ col: 4, row: 3 }, [card("a", 0, 0)])).toEqual({ col: 4, row: 3 });
  });

  it("moves off a card that already sits on the anchor, to a neighbouring cell", () => {
    const taken = [card("a", 4, 3)];
    const cell = nearestFreeGraphCell({ col: 4, row: 3 }, taken);
    expect(cell).not.toEqual({ col: 4, row: 3 });
    expect(Math.abs(cell.col - 4) + Math.abs(cell.row - 3)).toBe(1);
  });

  it("gives five cards made at one anchor five different places", () => {
    const cols: Col[] = [];
    for (let i = 0; i < 5; i++) {
      const cell = nearestFreeGraphCell({ col: 4, row: 3 }, cols);
      cols.push(card(`n${i}`, cell.col, cell.row));
    }
    const keys = new Set(cols.map((c) => `${c.index},${c.row}`));
    expect(keys.size).toBe(5);
  });

  it("treats a card nudged half a row off the anchor as still in the way", () => {
    const cell = nearestFreeGraphCell({ col: 4, row: 3 }, [card("a", 4, 3.4)]);
    expect(cell).not.toEqual({ col: 4, row: 3 });
  });

  it("never leaves the board", () => {
    const cell = nearestFreeGraphCell({ col: 0, row: 0 }, [card("a", 0, 0)]);
    expect(cell.col).toBeGreaterThanOrEqual(0);
    expect(cell.row).toBeGreaterThanOrEqual(0);
  });
});
