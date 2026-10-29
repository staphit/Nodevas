/**
 * The wires a node's `requires` expression implies but graph.yaml never drew.
 *
 * The expression is the executable condition and canvas wires are its visual
 * projection (docs/concepts.md). Every edit made on the board writes both, but
 * a hand-written graph.yaml can state `requires: n0001` with no `edges:` block.
 * Read as it is, such a file shows no wire, calls the dependent a start node
 * and lists no prerequisite, while the condition still holds the node back.
 *
 * The board therefore reads the graph through this projection: each node the
 * expression names gets a plain required wire when no edge of any kind joins
 * the two yet. Removing such a wire goes to the server like any other, which
 * prunes the condition it stands for.
 */

import { requiresNodeRefs } from "../../domain/graph/requires";
import type { Graph, GraphEdge } from "../../types";

/** The required wires `requires` names with no edge drawn for them. */
export function impliedRequiresEdges(graph: Graph | null | undefined): GraphEdge[] {
  const nodes = graph?.nodes ?? [];
  if (!nodes.some((node) => node.requires?.trim())) return [];
  const ids = new Set(nodes.map((node) => node.id));
  const drawn = new Set((graph?.edges ?? []).map((edge) => `${edge.from}\u0000${edge.to}`));
  const implied: GraphEdge[] = [];
  for (const node of nodes) {
    if (!node.requires?.trim()) continue;
    for (const from of new Set(requiresNodeRefs(node.requires))) {
      if (from === node.id || !ids.has(from)) continue;
      const key = `${from}\u0000${node.id}`;
      if (drawn.has(key)) continue;
      drawn.add(key);
      implied.push({ from, to: node.id });
    }
  }
  return implied;
}

const projected = new WeakMap<Graph, Graph>();

/**
 * The graph as the board reads it: the stored edges plus the wires `requires`
 * implies. The same graph object comes back when nothing is implied, and one
 * graph always projects to the same object, so memoised readers stay stable.
 */
export function withRequiresProjection<G extends Graph | null | undefined>(graph: G): G {
  if (!graph) return graph;
  const cached = projected.get(graph);
  if (cached) return cached as G;
  const implied = impliedRequiresEdges(graph);
  const result = implied.length ? { ...graph, edges: [...(graph.edges ?? []), ...implied] } : graph;
  projected.set(graph, result);
  return result as G;
}
