import { describe, expect, it } from "vitest";
import { COL_W, ROW_H, type Col, type Rect } from "./geometry";
import {
  NO_SNAP_TARGETS,
  buildSnapTargets,
  rectOfColumn,
  snapBoxesOfColumns,
  solveResizeSnap,
  solveSnap,
  unionRect,
} from "./snapping";

function column(id: string, index: number, row: number, width = 140, height = 60): Col {
  return { node: { id, title: id.toUpperCase() }, index, row, width, height };
}

function rect(left: number, top: number, width: number, height: number): Rect {
  return { left, top, right: left + width, bottom: top + height };
}

/** A box far from everything, so one axis can be tested without the other. */
const FAR = 10_000;

/** Standalone boxes, the way a group or a sticky note arrives. */
function boxes(rects: Rect[]) {
  return rects.map((rect, index) => ({ id: `box-${index}`, rect }));
}

describe("rectOfColumn", () => {
  it("centres the card on its cell", () => {
    expect(rectOfColumn(column("a", 0, 0, 140, 60))).toEqual({
      left: COL_W / 2 - 70,
      right: COL_W / 2 + 70,
      top: ROW_H / 2 - 30,
      bottom: ROW_H / 2 + 30,
    });
  });

  it("agrees with the box a group drawn around a selection uses", () => {
    const card = column("a", 3, 2, 200, 100);
    const box = rectOfColumn(card);
    expect(box.left).toBe(card.index * COL_W + COL_W / 2 - card.width / 2);
    expect(box.top).toBe(card.row * ROW_H + ROW_H / 2 - card.height / 2);
  });
});

describe("unionRect", () => {
  it("is null for nothing and the hull for many", () => {
    expect(unionRect([])).toBeNull();
    expect(unionRect([rect(0, 0, 10, 10), rect(40, 5, 10, 30)])).toEqual({
      left: 0,
      top: 0,
      right: 50,
      bottom: 35,
    });
  });
});

describe("buildSnapTargets", () => {
  it("leaves out the moving cards and sorts each axis", () => {
    const targets = buildSnapTargets(
      snapBoxesOfColumns([column("a", 0, 0), column("b", 4, 0), column("c", 8, 0)]),
      new Set(["a"]),
    );
    expect(targets.x).toHaveLength(6);
    expect(targets.y).toHaveLength(6);
    expect(targets.x.map((t) => t.value)).toEqual([...targets.x.map((t) => t.value)].sort((p, q) => p - q));
    const movingLeft = rectOfColumn(column("a", 0, 0)).left;
    expect(targets.x.some((t) => t.value === movingLeft)).toBe(false);
  });

  it("takes group and annotation boxes as well", () => {
    const decoration = rect(500, 0, 200, 100);
    const targets = buildSnapTargets(boxes([decoration]));
    expect(targets.x.map((t) => t.value)).toEqual([500, 600, 700]);
  });
});

describe("solveSnap", () => {
  const moving = rect(100, 0, 100, 50);

  it("passes the delta through when there is nothing to align to", () => {
    expect(solveSnap(moving, 7, -3, NO_SNAP_TARGETS, 6)).toEqual({ dx: 7, dy: -3, guides: [] });
  });

  it("pulls an edge onto a neighbour inside the threshold", () => {
    const targets = buildSnapTargets(boxes([rect(304, FAR, 40, 50)]));
    const result = solveSnap(moving, 200, 0, targets, 6);
    // left 300 → 304
    expect(result.dx).toBe(204);
    expect(result.dy).toBe(0);
    expect(result.guides).toEqual([
      expect.objectContaining({ axis: "x", position: 304 }),
    ]);
  });

  it("leaves a delta alone just outside the threshold", () => {
    const targets = buildSnapTargets(boxes([rect(307, FAR, 100, 50)]));
    expect(solveSnap(moving, 200, 0, targets, 6).dx).toBe(200);
  });

  it("prefers centre-to-centre over an edge at the same distance", () => {
    const targets = buildSnapTargets(boxes([
      rect(133, FAR, 40, 40), // centre 153, three from the moving centre
      rect(103, FAR + 500, 297, 40), // left 103, three from the moving left
    ]));
    const result = solveSnap(moving, 0, 0, targets, 6);
    expect(result.dx).toBe(3);
    expect(result.guides[0]).toMatchObject({ axis: "x", position: 153 });
  });

  it("solves the axes separately", () => {
    const targets = buildSnapTargets(boxes([rect(104, 1000, 100, 50)]));
    const result = solveSnap(moving, 0, 0, targets, 6);
    expect(result.dx).toBe(4);
    expect(result.dy).toBe(0);
  });

  it("draws one guide spanning every box on the line", () => {
    const targets = buildSnapTargets(boxes([
      rect(104, 400, 40, 50),
      rect(104, 900, 40, 50),
    ]));
    const guide = solveSnap(moving, 0, 0, targets, 6).guides[0];
    // The moving box (0..50) plus both neighbours (400..450, 900..950), padded.
    expect(guide).toEqual({ axis: "x", position: 104, from: -8, to: 958 });
  });

  it("uses the bounding box of a multi-card selection", () => {
    const targets = buildSnapTargets(boxes([rect(500, FAR, 104, 50)]));
    const bounds = unionRect([rect(0, 0, 100, 50), rect(500, 0, 100, 50)])!;
    // The union is 0..600; only its right edge is near the 604 it should take.
    const result = solveSnap(bounds, 0, 0, targets, 6);
    expect(result.dx).toBe(4);
  });

  it("scales with the threshold it is given, so zoom can divide it out", () => {
    const targets = buildSnapTargets(boxes([rect(104, FAR, 100, 50)]));
    expect(solveSnap(moving, 0, 0, targets, 6).dx).toBe(4);
    expect(solveSnap(moving, 0, 0, targets, 6 / 2).dx).toBe(0);
  });
});

describe("solveResizeSnap", () => {
  const card = rect(100, 0, 100, 50); // centre 150

  it("doubles the pull, because the far edge moves too", () => {
    const targets = buildSnapTargets(boxes([rect(204, FAR, 10, 10)]));
    const result = solveResizeSnap(card, 1, 0, targets, 6);
    expect(result.dWidth).toBe(8);
    expect(result.dHeight).toBe(0);
    expect(result.guides).toEqual([expect.objectContaining({ position: 204 })]);
  });

  it("grows when the left handle is pulled outwards", () => {
    const targets = buildSnapTargets(boxes([rect(96, FAR, 200, 10)]));
    expect(solveResizeSnap(card, -1, 0, targets, 6).dWidth).toBe(8);
  });

  it("changes the size by the pull alone when the corner is pinned", () => {
    const targets = buildSnapTargets(boxes([rect(204, FAR, 10, 10)]));
    expect(solveResizeSnap(card, 1, 0, targets, 6, "start").dWidth).toBe(4);
  });

  it("ignores an axis the handle does not resize", () => {
    const targets = buildSnapTargets(boxes([rect(204, 4, 10, 10)]));
    expect(solveResizeSnap(card, 1, 0, targets, 6).dHeight).toBe(0);
  });
});
