/**
 * Node metadata reducers [A-04].
 *
 * Pure mutations of a graph draft: no network, no store, no React. The command
 * runner clones the graph before calling these, so mutating in place is safe
 * and keeps the diff obvious.
 */

import type {
  EdgeRelation,
  NodeLinkRef,
  Graph,
  GraphNode,
  GraphUser,
  NodeKind,
  NodeStyle,
} from "../../types";
import { invalid, notFound } from "../errors";

import {
  booleanLogicGateForOutput,
  dependencyConnectionError,
  logicGateOwningEdge,
} from "./logicGate";
import { edgeRelation } from "./edgeStyle";
import { appendRequirement, editableGateOperator, topLevelOp } from "./requires";

export interface NodeMetadataPatch {
  title?: string;
  kind?: NodeKind;
  priority?: GraphNode["priority"];
  /** "YYYY-MM-DD" or "YYYY-MM-DDTHH:mm", local time. Empty clears it. */
  deadline?: string;
  tags?: string[];
}

const DEADLINE_PATTERN = /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?$/;

export function findNode(graph: Graph, nodeId: string): GraphNode {
  const node = graph.nodes?.find((candidate) => candidate.id === nodeId);
  if (!node) throw notFound(`節點「${nodeId}」`);
  return node;
}

/** Empty strings clear the field instead of writing `""` into the YAML. */
function blankToUndefined(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  return trimmed ? trimmed : undefined;
}

export function updateNodeMetadata(
  graph: Graph,
  nodeId: string,
  patch: NodeMetadataPatch,
): void {
  const node = findNode(graph, nodeId);
  if ("title" in patch) node.title = blankToUndefined(patch.title);
  if ("kind" in patch) node.kind = (blankToUndefined(patch.kind) as NodeKind) ?? undefined;
  if ("priority" in patch) {
    node.priority = blankToUndefined(patch.priority) as GraphNode["priority"];
  }
  if ("deadline" in patch) {
    const deadline = blankToUndefined(patch.deadline);
    if (deadline && !DEADLINE_PATTERN.test(deadline)) {
      throw invalid("死線格式須為 YYYY-MM-DD 或 YYYY-MM-DDTHH:mm。");
    }
    node.deadline = deadline;
  }
  if ("tags" in patch) {
    const tags = Array.from(
      new Set((patch.tags ?? []).map((tag) => tag.trim()).filter(Boolean)),
    );
    node.tags = tags.length > 0 ? tags : undefined;
  }
}

export function setNodeStyle(
  graph: Graph,
  nodeId: string,
  patch: Partial<NodeStyle>,
): void {
  findNode(graph, nodeId);
  graph.ui = graph.ui ?? {};
  graph.ui.nodeStyles = graph.ui.nodeStyles ?? {};
  const next: NodeStyle = { ...(graph.ui.nodeStyles[nodeId] ?? {}), ...patch };
  const empty =
    !next.width &&
    !next.height &&
    !next.color &&
    !next.textColor &&
    !next.borderColor &&
    !next.shape &&
    !next.align &&
    !next.valign;
  if (empty) {
    delete graph.ui.nodeStyles[nodeId];
  }
  else graph.ui.nodeStyles[nodeId] = next;
}

/**
 * Records what the author says about a node being a starting point. `undefined`
 * hands the decision back to the automatic rule — nothing points at it — so the
 * override is removed rather than stored as a value meaning "no opinion".
 */
export function setNodeEntryOverride(
  graph: Graph,
  nodeId: string,
  value: boolean | undefined,
): void {
  findNode(graph, nodeId);
  graph.ui = graph.ui ?? {};
  const overrides = graph.ui.entryOverrides ?? {};
  if (value === undefined) delete overrides[nodeId];
  else overrides[nodeId] = value;
  if (Object.keys(overrides).length === 0) delete graph.ui.entryOverrides;
  else graph.ui.entryOverrides = overrides;
}

function nextUserID(users: GraphUser[]): string {
  const used = new Set(users.map((user) => user.id));
  for (let sequence = 1; sequence <= 10_000; sequence++) {
    const candidate = `user-${String(sequence).padStart(4, "0")}`;
    if (!used.has(candidate)) return candidate;
  }
  throw invalid("使用者數量已達上限");
}

/**
 * Assigns by display name, creating the project user on first use.
 * Returns the user id, or `undefined` when the assignment was cleared.
 */
export function assignNodeByName(
  graph: Graph,
  nodeId: string,
  name: string,
): string | undefined {
  const node = findNode(graph, nodeId);
  const trimmed = name.trim();
  if (!trimmed) {
    node.assignee = undefined;
    return undefined;
  }
  graph.users = graph.users ?? [];
  let user = graph.users.find(
    (candidate) =>
      candidate.name.localeCompare(trimmed, undefined, { sensitivity: "accent" }) === 0,
  );
  if (!user) {
    user = { id: nextUserID(graph.users), name: trimmed };
    graph.users.push(user);
  }
  node.assignee = user.id;
  return user.id;
}

/** Bulk assignment from a canvas selection. Empty id clears the field. */
export function assignNodes(
  graph: Graph,
  nodeIds: string[],
  userId: string | undefined,
): void {
  if (userId && !(graph.users ?? []).some((user) => user.id === userId)) {
    throw notFound(`使用者「${userId}」`);
  }
  const targets = new Set(nodeIds);
  for (const node of graph.nodes ?? []) {
    if (targets.has(node.id)) node.assignee = userId || undefined;
  }
}

export function setUserEmail(
  graph: Graph,
  userId: string,
  email: string | undefined,
): void {
  const user = graph.users?.find((candidate) => candidate.id === userId);
  if (!user) throw notFound(`使用者「${userId}」`);
  user.email = blankToUndefined(email);
}

/**
 * Adds `sourceId` as a dependency of `targetId`: appends it to the target's
 * requires expression and records the edge. Rejects self-links, duplicates and
 * cycles, because those cannot be satisfied.
 */
export function addDependency(
  graph: Graph,
  sourceId: string,
  targetId: string,
  options: { operator?: string; relation?: EdgeRelation } = {},
): void {
  const target = findNode(graph, targetId);
  const edges = graph.edges ?? [];
  const gate = logicGateOwningEdge(graph, sourceId, targetId);
  if (gate) {
    throw invalid(`關係線「${sourceId} → ${targetId}」由邏輯閘「${gate.id}」控制。`);
  }
  const error = dependencyConnectionError(edges, sourceId, targetId);
  if (error) throw invalid(error);
  const relation = options.relation ?? "";
  if (!relation) {
    const owner = booleanLogicGateForOutput(graph, targetId);
    if (owner) {
      throw invalid(`「${targetId}」由邏輯閘「${owner.id}」控制，請改為編輯該閘門。`);
    }
    const chosen = options.operator ?? editableGateOperator(topLevelOp(target.requires));
    target.requires = appendRequirement(target.requires ?? "", sourceId, chosen);
  }
  graph.edges = [
    ...edges,
    {
      from: sourceId,
      to: targetId,
      ...(relation ? { relation } : {}),
    },
  ];
}

/**
 * Replaces a node's named links. The whole list is written at once: the form
 * edits labels and order in place, and a partial update would need a second
 * source of truth for what the list currently is.
 */
export function setNodeLinks(
  graph: Graph,
  nodeId: string,
  links: NodeLinkRef[],
): void {
  const node = findNode(graph, nodeId);
  const cleaned: NodeLinkRef[] = [];
  const seen = new Set<string>();
  for (const link of links.slice(0, 64)) {
    const target = link.node.trim();
    const label = link.label.trim();
    if (!target) throw invalid("連結必須指向一個節點。");
    if (!label) throw invalid("連結名稱不可空白。");
    const key = `${link.project ?? ""}/${target}`;
    if (seen.has(key)) continue;
    seen.add(key);
    cleaned.push({
      label,
      node: target,
      ...(link.project ? { project: link.project } : {}),
    });
  }
  node.links = cleaned.length ? cleaned : undefined;
}

/**
 * Writes a node's `requires` expression and keeps incoming edges in sync with
 * its node references — edges are the visual cache of the gate logic. Pass
 * `refs` as `null` when the expression could not be parsed: the text is stored
 * and the existing edges are left alone.
 */
export function setNodeRequires(
  graph: Graph,
  nodeId: string,
  requires: string,
  refs: string[] | null,
): void {
  const node = findNode(graph, nodeId);
  const owner = booleanLogicGateForOutput(graph, nodeId);
  if (owner) {
    throw invalid(`「${nodeId}」由邏輯閘「${owner.id}」控制，請改為編輯該閘門。`);
  }
  if (refs !== null) {
    for (const ref of refs) {
      const gate = logicGateOwningEdge(graph, ref, nodeId);
      if (gate) {
        throw invalid(`關係線「${ref} → ${nodeId}」由邏輯閘「${gate.id}」控制。`);
      }
    }
  }
  node.requires = requires || undefined;
  if (refs === null) return;
  const keep = new Set(refs);
  graph.edges = (graph.edges ?? []).filter(
    (edge) =>
      edge.to !== nodeId || edgeRelation(edge) !== "" || keep.has(edge.from),
  );
  for (const ref of refs) {
    const existing = graph.edges.find(
      (edge) => edge.from === ref && edge.to === nodeId,
    );
    if (existing) {
      // A node reference is executable, so it wins over a plain relation edge
      // for the same pair. Gate-owned relations were rejected above.
      existing.relation = undefined;
    } else if (graph.nodes?.some((node) => node.id === ref)) {
      graph.edges.push({ from: ref, to: nodeId });
    }
  }
}
