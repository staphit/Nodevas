/**
 * Dependency gates: the incoming edges of a node, grouped per target.
 *
 * Alongside first-class logic gates, a node whose dependencies are already
 * driven by one is skipped here and drawn by the gate itself.
 */

import { useMemo } from "react";
import { topLevelOp } from "../../store";
import type { EdgeLine, EdgeRelation, Graph, Status } from "../../types";
import type { GraphAnalysis } from "../../analysis";
import { edgeLine, edgeRelation } from "../../domain/graph/edgeStyle";
import { logicGateOutputs, logicGateRelation } from "../../domain/graph/logicGate";
import type { Gate } from "../canvas/GateWiring";
import type { Col } from "../canvas/geometry";

const edgeKey = (from: string, to: string) => `${from}\0${to}`;

export function useDependencyGates({
  graph,
  colOf,
  statuses,
  analysis,
}: {
  graph: Graph | null;
  colOf: Map<string, Col>;
  statuses: Record<string, Status>;
  analysis: GraphAnalysis;
}): Gate[] {
  return useMemo(() => {
    const edges = graph?.edges ?? [];
    // What a first-class gate draws for itself, and so must not be drawn twice.
    // A boolean gate owns its output's whole fan-in — it rewrites `requires` and
    // clears the incoming edges. A relation gate owns only the input→output
    // pairs it marks, so any other edge into the same node is still an ordinary
    // wire and still needs drawing here.
    const gateOwnedTargets = new Set<string>();
    const gateOwnedEdges = new Set<string>();
    for (const gate of graph?.ui?.logicGates ?? []) {
      const outputs = logicGateOutputs(gate);
      if (!logicGateRelation(gate.operator)) {
        for (const output of outputs) gateOwnedTargets.add(output);
        continue;
      }
      for (const output of outputs) {
        for (const input of gate.inputs) gateOwnedEdges.add(edgeKey(input, output));
      }
    }
    // A wire leaving a node that is itself out of play is drawn like a
    // deprecated one: the relation is unchanged, only the ink is.
    const mutedSource = (nodeId: string) =>
      statuses[nodeId] === "deprecated" || analysis.deprecatedNodeIds.has(nodeId);
    const byTarget = new Map<
      string,
      {
        from: Col;
        relation: EdgeRelation;
        line: Exclude<EdgeLine, "">;
        muted: boolean;
      }[]
    >();
    for (const e of edges) {
      if (gateOwnedTargets.has(e.to) || gateOwnedEdges.has(edgeKey(e.from, e.to))) continue;
      const f = colOf.get(e.from);
      const t = colOf.get(e.to);
      if (!f || !t) continue;
      byTarget.set(e.to, [
        ...(byTarget.get(e.to) ?? []),
        {
          from: f,
          relation: edgeRelation(e),
          line: edgeLine(e),
          muted: mutedSource(e.from),
        },
      ]);
    }
    return [...byTarget.entries()].map(([to, parents]) => ({
      to: colOf.get(to)!,
      parents,
      op: topLevelOp(colOf.get(to)!.node.requires) ?? (parents.length > 1 ? "and" : null),
    }));
  }, [graph, colOf, statuses, analysis.deprecatedNodeIds]);
}
