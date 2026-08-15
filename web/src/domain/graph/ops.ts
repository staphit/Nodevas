/**
 * Applying somebody else's graph ops [P2].
 *
 * When a peer moves a card, the server tells the room which ops it applied
 * rather than only that the file changed. Replaying them here moves that one
 * card, instead of reloading the whole graph and taking the reader's selection,
 * scroll position and open menus with it.
 *
 * This mirrors `applyGraphOp` in `internal/store/graph_ops.go`, and the pair
 * has to stay in step — which is why anything unexpected returns false rather
 * than guessing: the caller then reloads, which is what every change did before
 * this existed.
 */

import type { GraphOp } from "../../api";
import type { Graph } from "../../types";

function finite(value: number | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function ui(graph: Graph): NonNullable<Graph["ui"]> {
  graph.ui = graph.ui ?? {};
  return graph.ui;
}

function applyOne(graph: Graph, op: GraphOp): boolean {
  const nodes = graph.nodes ?? [];
  const node = op.nodeId ? nodes.find((item) => item.id === op.nodeId) : undefined;
  switch (op.kind) {
    case "move": {
      if (!node || !finite(op.x) || !finite(op.y)) return false;
      const layout = ui(graph);
      layout.positions = { ...(layout.positions ?? {}), [node.id]: { x: op.x, y: op.y } };
      return true;
    }
    case "node-size": {
      if (!node || !finite(op.width) || !finite(op.height)) return false;
      const layout = ui(graph);
      const styles = { ...(layout.nodeStyles ?? {}) };
      styles[node.id] = { ...styles[node.id], width: op.width, height: op.height };
      layout.nodeStyles = styles;
      return true;
    }
    case "node-metadata": {
      if (!node) return false;
      // Present means set, absent means leave alone — the same rule the server
      // op follows, and what lets two people edit different fields of one node.
      if (op.title !== undefined) node.title = op.title;
      if (op.nodeKind !== undefined) node.kind = op.nodeKind as typeof node.kind;
      if (op.priority !== undefined) node.priority = op.priority as typeof node.priority;
      if (op.assignee !== undefined) node.assignee = op.assignee;
      if (op.deadline !== undefined) node.deadline = op.deadline;
      if (op.tags !== undefined) node.tags = [...op.tags];
      return true;
    }
    case "add-edge":
    case "remove-edge":
    case "set-edge-style":
      // The Store also materializes node.requires and the node frontmatter for
      // these. Edge-only replay would create a transient split-brain graph, so
      // semantic ops are broadcast as graph-changed and an old server event
      // deliberately falls back to a reload.
      return false;
    case "timeline-order": {
      if (!op.order) return false;
      if (op.order.some((id) => !nodes.some((item) => item.id === id))) return false;
      ui(graph).timelineOrder = [...op.order];
      return true;
    }
    default:
      return false;
  }
}

/**
 * Replays a peer's ops onto a copy of the graph.
 *
 * Returns the new graph, or null when any op could not be applied — a kind
 * this client does not know, or one naming a node it has not heard of yet. All
 * or nothing, so a half-applied batch never reaches the screen.
 */
export function applyGraphOps(graph: Graph, ops: readonly GraphOp[]): Graph | null {
  if (ops.length === 0) return null;
  const copy: Graph = structuredClone(graph);
  for (const op of ops) {
    if (!applyOne(copy, op)) return null;
  }
  return copy;
}
