/**
 * Domain vocabulary [A-01].
 *
 * The wire shapes in `../types` mirror the Go engine and must keep their JSON
 * field names. This module gives those shapes their domain names so code and
 * UI copy can stop calling two different things "status":
 *
 * - **Lifecycle status** — what actually happened. Source of truth is the
 *   append-only journal (`run/journal.jsonl`); `state.json` is a derived cache.
 * - **Plan milestone** — what is expected to happen. Source of truth is
 *   `graph.yaml → ui.plans`; the milestone *type* is `ui.planStatuses`.
 *
 * Nothing here changes serialization: every alias is the wire type.
 */

import { translate } from "../i18n";
import { isBuiltinPlanStatus } from "../plan";
import type {
  BuiltinNodeKind,
  BuiltinPlanStatus,
  CustomNodeTypeId as WireCustomNodeTypeId,
  BuiltinStatus,
  CustomStatus,
  HistoryEvent,
  NodeState,
  PlanMilestone,
  PlanStatus,
  PlanStatusDefinition,
  Status,
  StatusDefinition,
} from "../types";

// ---------------------------------------------------------------------------
// Actual lifecycle (journal-owned)
// ---------------------------------------------------------------------------

/** Actual lifecycle state of a node. Wire name: `Status`. */
export type LifecycleStatus = Status;
/** Engine-defined lifecycle states. */
export type BuiltinLifecycleStatus = BuiltinStatus;
/** Project-defined lifecycle states (`graph.yaml → ui.customStatuses`). */
export type CustomLifecycleStatus = CustomStatus;
/** Project-defined lifecycle state definition. Wire name: `StatusDefinition`. */
export type LifecycleStatusDefinition = StatusDefinition;
/** One appended journal entry. Wire name: `HistoryEvent`. */
export type LifecycleEvent = HistoryEvent;
/** Latest lifecycle state of one node, derived from the journal. */
export type NodeLifecycleState = NodeState;

// ---------------------------------------------------------------------------
// Expected plan (graph.yaml-owned)
// ---------------------------------------------------------------------------

/** Expected milestone type. Wire name: `PlanStatus`. */
export type MilestoneType = PlanStatus;
/** Engine-defined milestone types. */
export type BuiltinMilestoneType = BuiltinPlanStatus;
/** Project-defined milestone type. Wire name: `PlanStatusDefinition`. */
export type MilestoneTypeDefinition = PlanStatusDefinition;
/** One expected milestone on a node. */
export type PlannedMilestone = PlanMilestone;

// ---------------------------------------------------------------------------
// Node types (graph.yaml-owned)
// ---------------------------------------------------------------------------

/** Engine-defined node type ids. */
export type BuiltinNodeType = BuiltinNodeKind;
/** Project-defined node type id (`graph.yaml → node_types`). */
export type CustomNodeTypeId = WireCustomNodeTypeId;

// ---------------------------------------------------------------------------
// UI copy
// ---------------------------------------------------------------------------

/**
 * Fixed UI wording (plan §5.4). Never label a plan milestone "狀態" and never
 * label a lifecycle status "計畫" — the two live in different files, are
 * written by different actions, and undo differently.
 */
export const TERMS = {
  lifecycleStatus: "term.lifecycleStatus",
  lifecycleEvent: "term.lifecycleEvent",
  planMilestone: "term.planMilestone",
  planSchedule: "term.planSchedule",
  milestoneType: "term.milestoneType",
  document: "term.document",
  workflow: "term.workflow",
  projectLayout: "term.projectLayout",
  uiPreference: "term.uiPreference",
} as const;

export type TermKey = keyof typeof TERMS;

/** A fixed term in the reader's language; TERMS holds catalog keys. */
export function term(key: TermKey): string {
  return translate(TERMS[key]);
}

/** Builtin lifecycle states, in engine progression order. */
export const BUILTIN_LIFECYCLE_STATUSES: readonly BuiltinLifecycleStatus[] = [
  "locked",
  "ready",
  "started",
  "in_progress",
  "done",
  "skipped",
  "failed",
];

/** Builtin milestone types, in schedule order. */
export const BUILTIN_MILESTONE_TYPES: readonly BuiltinMilestoneType[] = [
  "started",
  "in_progress",
  "done",
];

export function isCustomLifecycleStatus(
  value: string,
): value is CustomLifecycleStatus {
  return value.startsWith("custom-status-");
}

export function isBuiltinLifecycleStatus(
  value: string,
): value is BuiltinLifecycleStatus {
  return (BUILTIN_LIFECYCLE_STATUSES as readonly string[]).includes(value);
}

export function isBuiltinMilestoneType(value: string): value is BuiltinMilestoneType {
  return isBuiltinPlanStatus(value as PlanStatus);
}

export function isCustomMilestoneType(value: string): value is `custom-${string}` {
  return value.startsWith("custom-") && !isCustomLifecycleStatus(value);
}
