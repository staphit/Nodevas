import type { DSLCheckResult, Graph, Issue, Status } from "../types";
import { req } from "./http";
import { verifyGraphResponse } from "./verify";

export interface GraphResponse {
  graph: Graph;
  rev: string;
  statuses: Record<string, Status>;
  issues: Issue[] | null;
}

/**
 * One server-side graph command (POST /api/graph/ops). Each op names the
 * smallest thing it changes, so two people editing different parts of one board
 * do not collide over the whole file the way a PUT /api/graph baseRev does.
 * Mirrors the Go GraphOp struct.
 */
export interface GraphOp {
  kind:
    | "move"
    | "node-size"
    | "node-metadata"
    | "add-edge"
    | "remove-edge"
    | "set-edge-style"
    | "timeline-order";
  nodeId?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  title?: string;
  nodeKind?: string;
  priority?: string;
  assignee?: string;
  /** "" clears the restriction back to everyone. */
  writeAccess?: string;
  deadline?: string;
  tags?: string[];
  from?: string;
  to?: string;
  relation?: "" | "optional" | "deprecated";
  line?: "" | "solid" | "dashed" | "dotted";
  order?: string[];
}

export interface GraphOpsResponse {
  ok: boolean;
  graph: Graph;
  rev: string;
  statuses: Record<string, Status>;
  issues: Issue[] | null;
}

export const graphApi = {
  getGraph: () => req<GraphResponse>("/api/graph", undefined, verifyGraphResponse),
  putGraph: (graph: Graph, baseRev: string) =>
    req<{ ok: boolean; rev: string; issues: Issue[] | null }>("/api/graph", {
      method: "PUT",
      body: JSON.stringify({ graph, baseRev }),
    }),
  /** Per-node graph commands; carries no baseRev, so unrelated concurrent
   * edits no longer collide. Returns the server's merged graph. */
  graphOps: (ops: GraphOp[], peerId = "") =>
    req<GraphOpsResponse>("/api/graph/ops", {
      method: "POST",
      // Names the connection making the change, so the server can send the ops
      // to the rest of the room without bouncing them back here.
      headers: peerId ? { "X-Nodevas-Peer": peerId } : undefined,
      body: JSON.stringify({ ops }),
    }),
  checkDSL: (expr: string) =>
    req<DSLCheckResult>("/api/dsl/check", {
      method: "POST",
      body: JSON.stringify({ expr }),
    }),
};
