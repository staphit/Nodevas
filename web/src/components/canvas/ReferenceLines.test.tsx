import { act, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Graph } from "../../types";
import { ROW_H, type Col } from "./geometry";
import {
  MAX_REFERENCE_PAIRS,
  ReferenceLines,
  referencePairs,
  setHoveredReferenceCard,
} from "./ReferenceLines";

function board(graph: Graph): Map<string, Col> {
  return new Map(
    (graph.nodes ?? []).map((node, index) => [
      node.id,
      { node, row: index, index, width: 150, height: ROW_H - 12 },
    ]),
  );
}

const centerX = (column: Col) => 100 + column.index * 200;
const rowTop = (row: number) => row * ROW_H;

function draw(graph: Graph, activeIds: Set<string> = new Set()) {
  return render(
    <svg>
      <ReferenceLines
        graph={graph}
        colOf={board(graph)}
        centerX={centerX}
        rowTop={rowTop}
        activeIds={activeIds}
      />
    </svg>,
  );
}

const sample: Graph = {
  version: 1,
  nodes: [
    { id: "note", title: "角色卡", kind: "note" },
    { id: "scene", title: "第一場", kind: "scene", refs: ["note", "gone"] },
  ],
  edges: [],
};

describe("ReferenceLines", () => {
  it("draws one dotted curve per reference, with no arrowhead", () => {
    const { container } = draw(sample);
    const wires = container.querySelectorAll("path.reference-wire");
    // The dangling "gone" reference has nothing to draw to.
    expect(wires).toHaveLength(1);
    for (const wire of wires) {
      expect(wire.getAttribute("marker-end")).toBeNull();
      expect(wire.getAttribute("d")).toMatch(/^M .* Q /);
    }
    expect(container.querySelector("[marker-end]")).toBeNull();
    expect(container.querySelector('[data-reference="note->scene"]')).not.toBeNull();
  });

  it("lights the references of a selected or hovered card", () => {
    const { container, rerender } = draw(sample, new Set(["scene"]));
    expect(container.querySelector("path.reference-wire.active")).not.toBeNull();

    rerender(
      <svg>
        <ReferenceLines
          graph={sample}
          colOf={board(sample)}
          centerX={centerX}
          rowTop={rowTop}
          activeIds={new Set()}
        />
      </svg>,
    );
    expect(container.querySelector("path.reference-wire.active")).toBeNull();
    act(() => setHoveredReferenceCard("note"));
    expect(container.querySelector("path.reference-wire.active")).not.toBeNull();
    act(() => setHoveredReferenceCard(null));
    expect(container.querySelector("path.reference-wire.active")).toBeNull();
  });

  it("draws only the active pairs on a crowded board", () => {
    const count = MAX_REFERENCE_PAIRS + 1;
    const graph: Graph = {
      version: 1,
      nodes: [
        { id: "note", kind: "note" },
        ...Array.from({ length: count }, (_, index) => ({ id: `s${index}`, refs: ["note"] })),
      ],
      edges: [],
    };
    expect(referencePairs(graph, () => true)).toHaveLength(count);
    const { container } = draw(graph, new Set(["s7"]));
    expect(container.querySelectorAll("path.reference-wire")).toHaveLength(1);
  });

  it("draws nothing without references", () => {
    const { container } = draw({ version: 1, nodes: [{ id: "a" }], edges: [] });
    expect(container.querySelector(".reference-lines")).toBeNull();
  });
});
