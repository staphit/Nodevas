import { describe, expect, it } from "vitest";
import type { Graph } from "../../types";
import { applyGraphOps } from "./ops";

function graph(): Graph {
  return {
    version: 1,
    nodes: [
      { id: "a", title: "A" },
      { id: "b", title: "B" },
    ],
    edges: [],
    ui: {},
  };
}

describe("applyGraphOps", () => {
  it("moves a card without touching the rest of the graph", () => {
    const before = graph();
    const after = applyGraphOps(before, [{ kind: "move", nodeId: "a", x: 3, y: 4 }]);
    expect(after?.ui?.positions).toEqual({ a: { x: 3, y: 4 } });
    // The caller's graph is left alone: the store swaps the new one in.
    expect(before.ui?.positions).toBeUndefined();
  });

  it("sets only the metadata fields the op carries", () => {
    const after = applyGraphOps(graph(), [
      { kind: "node-metadata", nodeId: "a", title: "Renamed" },
    ]);
    expect(after?.nodes?.[0]).toMatchObject({ id: "a", title: "Renamed" });
    expect(after?.nodes?.[0].priority).toBeUndefined();
  });

  it("reloads for dependency ops instead of replaying only the edge half", () => {
    expect(
      applyGraphOps(graph(), [{ kind: "add-edge", from: "a", to: "b" }]),
    ).toBeNull();
    expect(
      applyGraphOps(graph(), [{ kind: "remove-edge", from: "a", to: "b" }]),
    ).toBeNull();
    expect(
      applyGraphOps(graph(), [
        { kind: "set-edge-style", from: "a", to: "b", relation: "optional" },
      ]),
    ).toBeNull();
  });

  it("resizes a card", () => {
    const after = applyGraphOps(graph(), [
      { kind: "node-size", nodeId: "a", width: 200, height: 90 },
    ]);
    expect(after?.ui?.nodeStyles?.a).toMatchObject({ width: 200, height: 90 });
  });

  it("refuses a kind it does not know, so the caller reloads instead", () => {
    expect(
      applyGraphOps(graph(), [{ kind: "invented" as never, nodeId: "a" }]),
    ).toBeNull();
  });

  it("refuses an op about a node this client has not seen", () => {
    expect(applyGraphOps(graph(), [{ kind: "move", nodeId: "ghost", x: 1, y: 1 }])).toBeNull();
  });

  it("is all or nothing, so a half-applied batch never reaches the screen", () => {
    expect(
      applyGraphOps(graph(), [
        { kind: "move", nodeId: "a", x: 1, y: 1 },
        { kind: "move", nodeId: "ghost", x: 2, y: 2 },
      ]),
    ).toBeNull();
  });

  it("refuses coordinates that are not finite", () => {
    expect(
      applyGraphOps(graph(), [{ kind: "move", nodeId: "a", x: Number.NaN, y: 0 }]),
    ).toBeNull();
  });
});
