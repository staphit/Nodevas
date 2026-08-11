import { describe, expect, it } from "vitest";
import { graphCommandToOps, graphOpsNeedServerGraph } from "./graphOps";

describe("graphCommandToOps", () => {
  it("turns a plain card move into one op per card", () => {
    expect(
      graphCommandToOps({
        type: "canvas.moveNodes",
        positions: { a: { x: 1, y: 2 }, b: { x: 3, y: 4 } },
      }),
    ).toEqual([
      { kind: "move", nodeId: "a", x: 1, y: 2 },
      { kind: "move", nodeId: "b", x: 3, y: 4 },
    ]);
  });

  it("keeps a board-sliding move on the whole-file path", () => {
    // Dragging past the top-left corner moves the groups, notes, gates and
    // wire vertices with the cards, and no op carries that. Sending only the
    // positions would apply half the command.
    expect(
      graphCommandToOps({
        type: "canvas.moveNodes",
        positions: { a: { x: 0, y: 0 } },
        shift: { columns: 0, rows: 0, x: 164, y: 80 },
      }),
    ).toBeNull();
  });

  it("only ops a style change that is exactly a resize", () => {
    expect(
      graphCommandToOps({
        type: "node.setStyle",
        nodeId: "a",
        patch: { width: 200, height: 90 },
      }),
    ).toEqual([{ kind: "node-size", nodeId: "a", width: 200, height: 90 }]);
    expect(
      graphCommandToOps({
        type: "node.setStyle",
        nodeId: "a",
        patch: { width: 200, height: 90, color: "#fff" },
      }),
    ).toBeNull();
  });

  it("sends the final relation style and waits for the canonical server graph", () => {
    const resultingGraph = {
      version: 1,
      nodes: [{ id: "a" }, { id: "b" }],
      edges: [{ from: "a", to: "b", relation: "optional" as const, line: "dotted" as const }],
      ui: {},
    };
    const ops = graphCommandToOps(
      {
        type: "canvas.setEdgeStyle",
        edges: [{ from: "a", to: "b" }],
        relation: "optional",
        line: "dotted",
      },
      resultingGraph,
    );
    expect(ops).toEqual([
      {
        kind: "set-edge-style",
        from: "a",
        to: "b",
        relation: "optional",
        line: "dotted",
      },
    ]);
    expect(graphOpsNeedServerGraph(ops)).toBe(true);
    expect(
      graphOpsNeedServerGraph([{ kind: "move", nodeId: "a", x: 1, y: 2 }]),
    ).toBe(false);
  });

  it("uses a semantic remove-edge op", () => {
    const ops = graphCommandToOps({ type: "canvas.removeEdge", from: "a", to: "b" });
    expect(ops).toEqual([{ kind: "remove-edge", from: "a", to: "b" }]);
    expect(graphOpsNeedServerGraph(ops)).toBe(true);
  });

  it("keeps operator-aware dependency creation on the whole-graph path", () => {
    // Store add-edge is the MCP's all-prerequisites/AND primitive. The Web
    // command can deliberately extend OR/XOR/NAND/NOR and must preserve the
    // reducer's exact expression through PUT instead of silently forcing AND.
    expect(
      graphCommandToOps({
        type: "node.addDependency",
        sourceId: "c",
        targetId: "a",
        operator: "or",
      }),
    ).toBeNull();
  });
});
