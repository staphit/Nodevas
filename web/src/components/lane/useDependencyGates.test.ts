import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { useDependencyGates } from "./useDependencyGates";
import type { GraphAnalysis } from "../../analysis";
import type { Col } from "../canvas/geometry";
import type { Graph, LogicGate, Status } from "../../types";

function col(id: string, index: number): Col {
  return { node: { id, title: id }, row: 0, index, width: 160, height: 60 };
}

const analysis = { deprecatedNodeIds: new Set<string>() } as unknown as GraphAnalysis;

function drawn(graph: Graph, statuses: Record<string, Status> = {}) {
  const ids = new Set<string>();
  for (const edge of graph.edges ?? []) {
    ids.add(edge.from);
    ids.add(edge.to);
  }
  for (const gate of graph.ui?.logicGates ?? []) {
    for (const input of gate.inputs) ids.add(input);
    for (const output of gate.outputs ?? []) ids.add(output);
  }
  const colOf = new Map([...ids].map((id, i) => [id, col(id, i)]));
  const { result } = renderHook(() =>
    useDependencyGates({ graph, colOf, statuses, analysis }),
  );
  return result.current.flatMap((gate) =>
    gate.parents.map((parent) => `${parent.from.node.id}->${gate.to.node.id}`),
  );
}

const relationGate: LogicGate = {
  id: "gate-1",
  operator: "optional",
  x: 0,
  y: 0,
  inputs: ["run", "jump"],
  outputs: ["target"],
};

describe("useDependencyGates", () => {
  it("leaves the pairs a relation gate marks to the gate itself", () => {
    expect(
      drawn({
        nodes: [],
        edges: [
          { from: "run", to: "target", relation: "optional" },
          { from: "jump", to: "target", relation: "optional" },
        ],
        ui: { logicGates: [relationGate] },
      } as unknown as Graph),
    ).toEqual([]);
  });

  it("still draws an ordinary edge into a node a relation gate also targets", () => {
    // The gate owns only its own input→output pairs, so a required edge from
    // somewhere else is a plain wire and nothing else will draw it.
    expect(
      drawn({
        nodes: [],
        edges: [
          { from: "run", to: "target", relation: "optional" },
          { from: "jump", to: "target", relation: "optional" },
          { from: "other", to: "target" },
        ],
        ui: { logicGates: [relationGate] },
      } as unknown as Graph),
    ).toEqual(["other->target"]);
  });

  it("leaves a boolean gate's whole fan-in to the gate", () => {
    // A boolean gate rewrites `requires` and clears the incoming edges, so
    // every wire into its output belongs to it.
    expect(
      drawn({
        nodes: [],
        edges: [{ from: "other", to: "target" }],
        ui: {
          logicGates: [
            { id: "gate-1", operator: "and", x: 0, y: 0, inputs: ["run", "jump"], output: "target" },
          ],
        },
      } as unknown as Graph),
    ).toEqual([]);
  });
});
