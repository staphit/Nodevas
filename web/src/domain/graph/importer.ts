/**
 * Bulk import [A-04].
 *
 * Applies a whole imported canvas in one revision: placement, styling, metadata
 * and dependencies for every imported node. The nodes themselves are created
 * through the node API first — this only wires up what lives in `graph.yaml`.
 *
 * One command rather than N per-node commands, so an import is one undo step
 * and one write instead of hundreds.
 */

import type { EdgeRelation, Graph, GraphNode, NodeStyle } from "../../types";
import { invalid } from "../errors";

export interface ImportedNode {
  /** Id of the node already created through `POST /api/nodes`. */
  id: string;
  position: { x: number; y: number };
  style?: NodeStyle;
  priority?: GraphNode["priority"];
  /** Existing user id; ignored when the project has no such user. */
  assignee?: string;
  tags?: string[];
}

export interface ImportedEdge {
  from: string;
  to: string;
  relation?: EdgeRelation;
}

export interface ImportPayload {
  nodes: ImportedNode[];
  edges: ImportedEdge[];
}

const MAX_IMPORT_NODES = 1000;

export function applyImport(graph: Graph, payload: ImportPayload): void {
  if (payload.nodes.length > MAX_IMPORT_NODES) {
    throw invalid(`單次最多匯入 ${MAX_IMPORT_NODES} 個節點。`);
  }
  graph.ui = graph.ui ?? {};
  graph.ui.positions = graph.ui.positions ?? {};
  graph.ui.nodeStyles = graph.ui.nodeStyles ?? {};

  for (const imported of payload.nodes) {
    const node = graph.nodes?.find((candidate) => candidate.id === imported.id);
    if (node) {
      node.priority = imported.priority;
      node.assignee = (graph.users ?? []).some((user) => user.id === imported.assignee)
        ? imported.assignee
        : undefined;
      node.tags = imported.tags?.length ? imported.tags : undefined;
    }
    graph.ui.positions[imported.id] = {
      x: Math.max(0, imported.position.x),
      y: Math.max(0, imported.position.y),
    };
    if (imported.style && (imported.style.width || imported.style.height || imported.style.color)) {
      graph.ui.nodeStyles[imported.id] = imported.style;
    }
  }

  const edges = payload.edges.filter((edge) => edge.from !== edge.to);
  graph.edges = [
    ...(graph.edges ?? []),
    ...edges.map((edge) => ({
      from: edge.from,
      to: edge.to,
      ...(edge.relation ? { relation: edge.relation } : {}),
    })),
  ];

  // Required incoming edges become the target's condition, so an
  // imported graph behaves like one built in the editor.
  const requiredByTarget = new Map<string, string[]>();
  for (const edge of edges) {
    if (edge.relation) continue;
    requiredByTarget.set(edge.to, [...(requiredByTarget.get(edge.to) ?? []), edge.from]);
  }
  for (const [targetId, sourceIds] of requiredByTarget) {
    const target = graph.nodes?.find((node) => node.id === targetId);
    if (target) target.requires = [...new Set(sourceIds)].join(" and ");
  }
}
