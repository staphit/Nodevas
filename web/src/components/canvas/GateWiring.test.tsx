import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { EdgeRelation } from "../../types";
import { GateWiring, showsGateBadge, type Gate } from "./GateWiring";
import type { Col } from "./geometry";

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
