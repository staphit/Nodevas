// The tasks that wait on others to finish: work no agent can take up yet.
// Decided from analyzeGraph().blocked, never from the server's status:
// "ready" only means nobody has touched the node yet (internal/engine/
// ready.go), and an untouched node can still be blocked.

import { isSettledStatus, type GraphAnalysis } from "../analysis";
import { nodeTypeOf } from "../domain/graph/nodeTypes";
import type { Graph, Status } from "../types";

export interface WaitingTask {
  nodeId: string;
  title: string;
  /** The node's type id, for its label chip. */
  kind: string;
  /** What it waits on, by node id. */
  blockers: string[];
}

/** Unsettled work nodes something blocks, in graph order. Start, End, Note
 * and any type that does not count as work are left out. */
export function waitingOnPrerequisites(
  graph: Graph | null | undefined,
  statuses: Record<string, Status>,
  analysis: Pick<GraphAnalysis, "blocked">,
): WaitingTask[] {
  const custom = graph?.ui?.customStatuses ?? [];
  const waiting: WaitingTask[] = [];
  for (const node of graph?.nodes ?? []) {
    if (!nodeTypeOf(graph, node).countsInProgress) continue;
    if (isSettledStatus(statuses[node.id] ?? "ready", custom)) continue;
    const blockers = analysis.blocked.get(node.id);
    if (!blockers) continue;
    waiting.push({ nodeId: node.id, title: node.title ?? "", kind: node.kind ?? "", blockers: [...blockers] });
  }
  return waiting;
}
