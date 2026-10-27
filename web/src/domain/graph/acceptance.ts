// Acceptance criteria and the project's agent-role/workflow settings, as graph
// reducers. Mirrors internal/engine/acceptance.go. The hub re-checks every
// rule (and who may write), so these only stop a bad write leaving the page.
//
// Everything here is pure. Reducers throw `CommandError`.

import type { Acceptance, AcceptanceItem, AgentRoleDef, Graph, Workflow } from "../../types";
import { invalid, notFound } from "../errors";
import { nodeTypeOf } from "./nodeTypes";
import { validateAgentRoleDefs } from "./roles";
import { validateWorkflow } from "./stages";

export const MAX_ACCEPTANCE_ITEMS = 30;
export const MAX_ACCEPTANCE_TEXT = 400;
const ITEM_ID = /^a[0-9]{1,3}$/;

/** Whether a node of this type carries acceptance criteria (agent-worked types). */
export function hasAcceptance(graph: Graph | null | undefined, kind: string | undefined): boolean {
  const mode = nodeTypeOf(graph, { kind: kind as never }).agentMode;
  return mode === "dispatch" || mode === "autopilot";
}

/** Whether a node of this type may override its project's workflow stages. */
export function hasWorkflowOverride(graph: Graph | null | undefined, kind: string | undefined): boolean {
  return nodeTypeOf(graph, { kind: kind as never }).agentMode === "autopilot";
}

/** The next free item id: one past the highest "aN" in use. */
export function nextAcceptanceItemID(items: readonly AcceptanceItem[]): string {
  let max = 0;
  for (const item of items) {
    const match = /^a(\d+)$/.exec(item.id);
    if (match) max = Math.max(max, Number(match[1]));
  }
  return `a${max + 1}`;
}

/** Throws with the first problem in an item list. */
export function validateAcceptanceItems(items: readonly AcceptanceItem[]): void {
  if (items.length > MAX_ACCEPTANCE_ITEMS) throw invalid(`驗收條件最多 ${MAX_ACCEPTANCE_ITEMS} 條。`);
  const seen = new Set<string>();
  for (const item of items) {
    if (!ITEM_ID.test(item.id)) throw invalid(`驗收條件編號 ${item.id} 不正確。`);
    if (seen.has(item.id)) throw invalid(`驗收條件編號 ${item.id} 重複。`);
    seen.add(item.id);
    const length = [...item.text].length;
    if (length === 0 || length > MAX_ACCEPTANCE_TEXT || /[\r\n]/.test(item.text)) {
      throw invalid(`每條驗收條件需為 1–${MAX_ACCEPTANCE_TEXT} 字的一行。`);
    }
  }
}

function asInvalid(check: () => void): void {
  try {
    check();
  } catch (e) {
    throw invalid(e instanceof Error ? e.message : String(e));
  }
}

function findNode(graph: Graph, nodeId: string) {
  const node = (graph.nodes ?? []).find((candidate) => candidate.id === nodeId);
  if (!node) throw notFound(`節點 ${nodeId} `);
  return node;
}

/** Replaces a node's criteria; null or no items clears them. Lock and author stay. */
export function setAcceptance(graph: Graph, nodeId: string, acceptance: Acceptance | null): void {
  const node = findNode(graph, nodeId);
  if (!hasAcceptance(graph, node.kind)) throw invalid("這種節點類型沒有驗收條件。");
  const items = acceptance?.items ?? [];
  validateAcceptanceItems(items);
  if (items.length === 0 && !node.acceptance?.locked) {
    delete node.acceptance;
    return;
  }
  node.acceptance = { ...node.acceptance, ...acceptance, items: items.map((item) => ({ ...item })) };
}

/** A Goal's (autopilot type's) stage override; null drops it. */
export function setNodeWorkflow(graph: Graph, nodeId: string, workflow: Workflow | null): void {
  const node = findNode(graph, nodeId);
  if (!hasWorkflowOverride(graph, node.kind)) throw invalid("只有自動推進類型的節點可以覆寫階段。");
  if (!workflow) {
    delete node.workflow;
    return;
  }
  asInvalid(() => validateWorkflow(workflow));
  node.workflow = structuredClone(workflow);
}

/** The project's workflow stages; null is legacy. */
export function setProjectWorkflow(graph: Graph, workflow: Workflow | null): void {
  if (!workflow) {
    graph.workflow = null;
    return;
  }
  asInvalid(() => validateWorkflow(workflow));
  graph.workflow = structuredClone(workflow);
}

/** The project's sparse agent-role list. */
export function setAgentRoles(graph: Graph, agentRoles: readonly AgentRoleDef[]): void {
  asInvalid(() => validateAgentRoleDefs(agentRoles));
  graph.agentRoles = structuredClone([...agentRoles]);
}
