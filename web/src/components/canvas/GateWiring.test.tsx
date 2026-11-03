import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { EdgeRelation } from "../../types";
import { GATE_W, GateWiring, gateLayout, showsGateBadge, type Gate } from "./GateWiring";
import { COL_W, ROW_H, type Col, type Rect } from "./geometry";
import { obstacleIndex } from "./obstacleIndex";

function col(id: string, index: number, requires?: string): Col {
  return {
    node: { id, title: id, ...(requires ? { requires } : {}) },
    row: index === 2 ? 1 : 0,
    index,
    width: 160,
    height: 60,
  } as Col;
}

function gate(
  op: string | null,
  requires: string | undefined,
  relations: EdgeRelation[] = ["", ""],
): Gate {
  return {
    to: col("target", 2, requires),
    parents: relations.map((relation, index) => ({
      from: col(`source-${index}`, index),
      relation,
      line: relation === "" ? "solid" : "dashed",
      muted: false,
    })),
    op,
  };
}

function renderGate(value: Gate) {
  return render(
    <svg>
      <GateWiring
        gate={value}
        centerX={(c) => c.index * 200 + 80}
        rowTop={(row) => row * 120}
        onPositionChange={() => {}}
        selectedEdges={new Set()}
        onSelectEdge={() => {}}
        onOpenEdgeMenu={() => {}}
        getWireVertices={() => []}
        onWireVerticesChange={() => {}}
        selectedVertex={null}
        onSelectVertex={() => {}}
        obstacles={[]}
        onEditGate={() => {}}
        onDeleteGate={() => {}}
      />
    </svg>,
  );
}

describe("showsGateBadge", () => {
  it("draws the default all-of fan-in as plain wires", () => {
    expect(showsGateBadge(gate("and", "source-0 and source-1"))).toBe(false);
    // Two edges and no expression yet still mean "all of them".
    expect(showsGateBadge(gate("and", undefined))).toBe(false);
  });

  it("keeps the badge for every non-default condition", () => {
    expect(showsGateBadge(gate("or", "source-0 or source-1"))).toBe(true);
    expect(showsGateBadge(gate("xor", "source-0 xor source-1"))).toBe(true);
    expect(showsGateBadge(gate("nand", "source-0 nand source-1"))).toBe(true);
    expect(showsGateBadge(gate("nor", "source-0 nor source-1"))).toBe(true);
    // AND at the top, but an OR group inside it.
    expect(
      showsGateBadge(gate("and", "(source-0 or source-1) and source-2")),
    ).toBe(true);
    // A relation other than required changes the meaning of the fan-in.
    expect(
      showsGateBadge(gate("and", "source-0", ["", "optional"])),
    ).toBe(true);
    // A lone optional parent is labelled with its relation.
    expect(showsGateBadge(gate(null, undefined, ["optional"]))).toBe(true);
  });

  it("keeps the badge on an AND left with too few inputs, so it can be flagged and deleted", () => {
    expect(showsGateBadge(gate("and", "source-0 and gone", [""]))).toBe(true);
  });

  it("has nothing to show for a single required dependency", () => {
    expect(showsGateBadge(gate(null, "source-0", [""]))).toBe(false);
  });
});

describe("GateWiring", () => {
  it("renders no gate label for the default fan-in", () => {
    const { container } = renderGate(gate("and", "source-0 and source-1"));
    expect(container.querySelector(".gate-label")).toBeNull();
    expect(container.querySelector("g.gate")).toBeNull();
    // Each prerequisite is still drawn, straight into the target.
    expect(container.querySelectorAll(".dependency-line").length).toBe(2);
  });

  it("labels an OR fan-in", () => {
    const { container } = renderGate(gate("or", "source-0 or source-1"));
    expect(container.querySelector(".gate-label")?.textContent).toBe("Any · OR");
  });
});

/**
 * Three cards side by side on the board's grid, the last waiting on either of
 * the other two: the only room between its inputs and itself is the gap
 * between two columns, far narrower than a gate box.
 */
describe("a gate between neighbouring columns", () => {
  const card = (id: string, index: number, requires?: string): Col => ({
    node: { id, title: id, ...(requires ? { requires } : {}) },
    row: 0,
    index,
    width: 152,
    height: 68,
  });
  const centerX = (c: Col) => c.index * COL_W + COL_W / 2;
  const rowTop = (row: number) => row * ROW_H;
  const cards = [card("a", 0), card("b", 1), card("c", 2, "b or a")];
  const obstacles: Rect[] = cards.map((c) => ({
    left: centerX(c) - c.width / 2,
    right: centerX(c) + c.width / 2,
    top: rowTop(c.row) + ROW_H / 2 - c.height / 2,
    bottom: rowTop(c.row) + ROW_H / 2 + c.height / 2,
  }));
  const neighbours: Gate = {
    to: cards[2],
    parents: [cards[1], cards[0]].map((from) => ({
      from,
      relation: "",
      line: "solid",
      muted: false,
    })),
    op: "or",
  };
  // The gap between card b's right edge and card c's left edge.
  const gapLeft = obstacles[1].right;
  const gapRight = obstacles[2].left;

  it("sits in the gap between the cards, as a compact box", () => {
    const layout = gateLayout(
      neighbours,
      centerX,
      rowTop,
      undefined,
      undefined,
      obstacleIndex(obstacles),
    );
    expect(layout.center.x).toBeCloseTo((gapLeft + gapRight) / 2);
    expect(layout.center.y).toBeCloseTo(ROW_H / 2);
    expect(layout.compact).toBe(true);
  });

  it("names just the operator, in a box that barely reaches into the cards", () => {
    const { container } = render(
      <svg>
        <GateWiring
          gate={neighbours}
          centerX={centerX}
          rowTop={rowTop}
          onPositionChange={() => {}}
          selectedEdges={new Set()}
          onSelectEdge={() => {}}
          onOpenEdgeMenu={() => {}}
          getWireVertices={() => []}
          onWireVerticesChange={() => {}}
          selectedVertex={null}
          onSelectVertex={() => {}}
          obstacles={obstacles}
          onEditGate={() => {}}
          onDeleteGate={() => {}}
        />
      </svg>,
    );
    expect(container.querySelector(".gate-label")?.textContent).toBe("OR");
    const box = container.querySelector(".gate-node")!;
    const width = Number(box.getAttribute("width"));
    const left = Number(box.getAttribute("x"));
    expect(width).toBeLessThan(GATE_W / 2);
    // No more than a few pixels over either card.
    expect(left).toBeGreaterThan(gapLeft - 10);
    expect(left + width).toBeLessThan(gapRight + 10);
  });

  it("keeps the full box where there is room for it", () => {
    const apart = [card("a", 0), card("b", 1), card("c", 4, "b or a")];
    const layout = gateLayout(
      { ...neighbours, to: apart[2] },
      centerX,
      rowTop,
      undefined,
      undefined,
      obstacleIndex(obstacles.slice(0, 2)),
    );
    expect(layout.compact).toBe(false);
  });
});
