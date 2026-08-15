/**
 * Expected milestone reducers [A-04].
 *
 * Owns `graph.yaml → ui.plans`. One milestone per type per node: scheduling a
 * type that already exists replaces it, which is what every existing caller
 * did by hand. Nothing here touches the journal.
 */

import type { Graph, PlanMilestone } from "../../types";
import { invalid } from "../errors";
import type { MilestoneType } from "../glossary";
import { isBuiltinMilestoneType } from "../glossary";
import { findNode } from "./node";

export interface PlanMilestoneInput {
  type: MilestoneType;
  /** Local calendar date, `YYYY-MM-DD`. */
  date: string;
  /** Optional local time, `HH:mm`; milestones sort by it inside a day. */
  time?: string;
  note?: string;
}

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const TIME_PATTERN = /^\d{2}:\d{2}$/;

export function sortMilestones(plans: PlanMilestone[]): PlanMilestone[] {
  return [...plans].sort(
    (a, b) => a.date.localeCompare(b.date) || (a.time ?? "").localeCompare(b.time ?? ""),
  );
}

export function nodeMilestones(graph: Graph, nodeId: string): PlanMilestone[] {
  return graph.ui?.plans?.[nodeId] ?? [];
}

function assertDate(date: string): void {
  if (!DATE_PATTERN.test(date)) throw invalid("日期格式須為 YYYY-MM-DD。");
}

function assertTime(time: string | undefined): void {
  if (time !== undefined && time !== "" && !TIME_PATTERN.test(time)) {
    throw invalid("時間格式須為 HH:mm。");
  }
}

export function upsertPlanMilestone(
  graph: Graph,
  nodeId: string,
  input: PlanMilestoneInput,
): void {
  findNode(graph, nodeId);
  assertDate(input.date);
  assertTime(input.time);
  graph.ui = graph.ui ?? {};
  graph.ui.plans = graph.ui.plans ?? {};
  const current = graph.ui.plans[nodeId] ?? [];
  const note = input.note?.trim();
  graph.ui.plans[nodeId] = sortMilestones([
    ...current.filter((plan) => plan.status !== input.type),
    {
      status: input.type,
      date: input.date,
      ...(input.time ? { time: input.time } : {}),
      ...(note ? { note } : {}),
    },
  ]);
}

/** Reschedules an existing milestone, keeping its note. Used by timeline drag. */
export function movePlanMilestone(
  graph: Graph,
  nodeId: string,
  type: MilestoneType,
  date: string,
  time?: string,
): void {
  assertDate(date);
  assertTime(time);
  const current = graph.ui?.plans?.[nodeId];
  const existing = current?.find((plan) => plan.status === type);
  if (!current || !existing) throw invalid("找不到要移動的預期里程碑。");
  graph.ui!.plans![nodeId] = sortMilestones(
    current.map((plan) => {
      if (plan.status !== type) return plan;
      const next: PlanMilestone = { ...plan, date };
      // `undefined` keeps the current time; "" clears it.
      if (time !== undefined) {
        if (time) next.time = time;
        else delete next.time;
      }
      return next;
    }),
  );
}

/** Removes one milestone; drops the node's entry once it has none left. */
export function removePlanMilestone(
  graph: Graph,
  nodeId: string,
  type: MilestoneType,
): void {
  const current = graph.ui?.plans?.[nodeId];
  if (!current) return;
  const remaining = current.filter((plan) => plan.status !== type);
  if (remaining.length) graph.ui!.plans![nodeId] = remaining;
  else delete graph.ui!.plans![nodeId];
}

/** Drops every milestone of a node. Used when a node is deleted. */
export function clearNodePlans(graph: Graph, nodeId: string): void {
  if (graph.ui?.plans) delete graph.ui.plans[nodeId];
}

/**
 * Schedule sanity check for builtin types: 開始 ≤ 預計進行 ≤ 死線.
 * Advisory — the command layer does not block it, because a user correcting one
 * end of a schedule legitimately passes through an inconsistent state.
 */
export function planOrderIssue(
  plans: PlanMilestone[],
  type: MilestoneType,
  date: string,
): string | null {
  if (!isBuiltinMilestoneType(type)) return null;
  const start = type === "started" ? date : plans.find((p) => p.status === "started")?.date;
  const progress =
    type === "in_progress" ? date : plans.find((p) => p.status === "in_progress")?.date;
  const done = type === "done" ? date : plans.find((p) => p.status === "done")?.date;
  if (start && progress && start > progress) return "預計進行日期不可早於開始日期。";
  if (progress && done && progress > done) return "死線不可早於預計進行日期。";
  if (start && done && start > done) return "死線不可早於開始日期。";
  return null;
}
