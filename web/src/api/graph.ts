import type { Acceptance, AgentRoleDef, DSLCheckResult, Graph, Issue, Status, Workflow } from "../types";
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
    | "timeline-order"
    | "add-ref"
    | "remove-ref"
    | "acceptance"
    | "node-workflow"
    | "workflow"
    | "agent-roles";
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
  /** add-ref / remove-ref: the node `nodeId` uses as context. */
  ref?: string;
  /** acceptance: the node's whole new criteria list (lock and author as read). */
  acceptance?: Acceptance;
  /** workflow (project) / node-workflow (a Goal's override): null drops it. */
  workflow?: Workflow | null;
  /** agent-roles: the project's whole sparse role list. */
  agentRoles?: AgentRoleDef[];
}

export interface GraphOpsResponse {
  ok: boolean;
  graph: Graph;
  rev: string;
  statuses: Record<string, Status>;
  issues: Issue[] | null;
}

/** One node of POST /api/graph/subgraph (store.SubgraphNode). */
export interface SubgraphNodeSpec {
  key: string;
  title: string;
  kind?: string;
  body?: string;
  /** Keys of this call, or existing node ids, that finish first. */
  dependsOn?: string[];
  /** Keys or node ids of Notes it uses as context. */
  refs?: string[];
}

/** POST /api/graph/subgraph and its dry run /subgraph/check. */
export interface SubgraphSpec {
  nodes: SubgraphNodeSpec[];
  /** An existing node (to) waiting for one of this call's (from). */
  edges?: { from: string; to: string }[];
  /** Grid cells by key (graph.ui.positions units): col 0..499, row 0..200. */
  positions?: Record<string, { col: number; row: number }>;
  /** Records the call for /api/graph/batches/:id/undo. */
  batch?: { via: string };
}

export interface SubgraphResult {
  ok: boolean;
  /** Key → node id (provisional on a dry run). */
  ids: Record<string, string>;
  created: { key: string; id: string; title: string; kind: string }[];
  rev: string;
  issues: Issue[] | null;
  dependencies: number;
  references: number;
  /** Commit with `batch` only. */
  batchId?: string;
  /** False: the nodes exist but no batch record was written (no undo). */
  undoable?: boolean;
}

export interface BatchRefusal {
  id: string;
  /** changed, status, claimed, busy or denied. */
  code: string;
  reason: string;
}

export interface BatchUndoResult {
  ok: boolean;
  trashed: string[] | null;
  refused: BatchRefusal[] | null;
}

export interface BatchRedoResult {
  ok: boolean;
  restored: string[] | null;
  skipped: BatchRefusal[] | null;
  links: number;
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
  /** Dry run: the answer a commit would give now; writes nothing. */
  checkSubgraph: (spec: SubgraphSpec) =>
    req<SubgraphResult>("/api/graph/subgraph/check", {
      method: "POST",
      body: JSON.stringify(spec),
    }),
  createSubgraph: (spec: SubgraphSpec) =>
    req<SubgraphResult>("/api/graph/subgraph", {
      method: "POST",
      body: JSON.stringify(spec),
    }),
  /** Trashes the batch's nodes nobody changed since; lists the ones it kept. */
  undoBatch: (batchId: string) =>
    req<BatchUndoResult>(`/api/graph/batches/${encodeURIComponent(batchId)}/undo`, {
      method: "POST",
    }),
  /** Restores what the undo trashed, and the links into existing nodes. */
  redoBatch: (batchId: string) =>
    req<BatchRedoResult>(`/api/graph/batches/${encodeURIComponent(batchId)}/redo`, {
      method: "POST",
    }),
  checkDSL: (expr: string) =>
    req<DSLCheckResult>("/api/dsl/check", {
      method: "POST",
      body: JSON.stringify({ expr }),
    }),
};
