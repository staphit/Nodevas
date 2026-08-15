/**
 * Impact checks [A-04].
 *
 * Answers "what breaks if I delete this?" before a destructive settings edit.
 * Pure reads — nothing here mutates.
 */

import type { Graph, RunState, Status } from "../types";
import { logicGateOutputs } from "./graph/logicGate";
import type { CustomLifecycleStatus, MilestoneType } from "./glossary";

export interface LifecycleStatusUsage {
  /** Nodes currently sitting in this state. */
  currentNodes: string[];
  /** Journal events that mention it, including historical ones. */
  events: number;
}

export function lifecycleStatusUsage(
  statuses: Record<string, Status>,
  runState: RunState | null,
  id: CustomLifecycleStatus,
): LifecycleStatusUsage {
  const currentNodes = Object.entries(statuses)
    .filter(([, status]) => status === id)
    .map(([nodeId]) => nodeId);
  const events = (runState?.history ?? []).filter(
    (event) => event.to === id || event.from === id,
  ).length;
  return { currentNodes, events };
}

export interface MilestoneTypeUsage {
  /** Nodes with a milestone of this type scheduled. */
  nodes: string[];
  milestones: number;
}

export function milestoneTypeUsage(graph: Graph, id: MilestoneType): MilestoneTypeUsage {
  const nodes: string[] = [];
  let milestones = 0;
  for (const [nodeId, plans] of Object.entries(graph.ui?.plans ?? {})) {
    const count = plans.filter((plan) => plan.status === id).length;
    if (count === 0) continue;
    nodes.push(nodeId);
    milestones += count;
  }
  return { nodes, milestones };
}

/** Nodes assigned to a project user. */
export function assigneeUsage(graph: Graph, userId: string): string[] {
  return (graph.nodes ?? [])
    .filter((node) => node.assignee === userId)
    .map((node) => node.id);
}

/** Every reference that would dangle if the node were removed. */
export function nodeReferenceUsage(
  graph: Graph,
  nodeId: string,
): { edges: number; requires: string[]; logicGates: string[]; milestones: number } {
  const edges = (graph.edges ?? []).filter(
    (edge) => edge.from === nodeId || edge.to === nodeId,
  ).length;
  const requires = (graph.nodes ?? [])
    .filter(
      (node) =>
        node.id !== nodeId &&
        new RegExp(`\\b${nodeId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\b`).test(
          node.requires ?? "",
        ),
    )
    .map((node) => node.id);
  const logicGates = (graph.ui?.logicGates ?? [])
    .filter(
      (gate) =>
        logicGateOutputs(gate).includes(nodeId) || gate.inputs.includes(nodeId),
    )
    .map((gate) => gate.id);
  const milestones = (graph.ui?.plans?.[nodeId] ?? []).length;
  return { edges, requires, logicGates, milestones };
}
