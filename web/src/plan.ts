import type {
  BuiltinPlanStatus,
  PlanMilestone,
  PlanStatus,
  PlanStatusDefinition,
} from "./types";

// Creation options. "in_progress" remains readable for existing plans while
// new planning uses the started/done lifecycle markers.
export const BUILTIN_PLAN_STATUSES: {
  status: BuiltinPlanStatus;
  label: string;
}[] = [
  { status: "started", label: "開始" },
  { status: "done", label: "死線" },
];

export const BUILTIN_PLAN_LABELS: Record<BuiltinPlanStatus, string> = {
  started: "開始",
  in_progress: "預計進行中",
  done: "死線",
};

export const BUILTIN_PLAN_SHORT_LABELS: Record<BuiltinPlanStatus, string> = {
  started: "開始",
  in_progress: "進行中",
  done: "死線",
};

export function isBuiltinPlanStatus(
  status: PlanStatus,
): status is BuiltinPlanStatus {
  return (
    status === "started" || status === "in_progress" || status === "done"
  );
}

export function planStatusLabel(
  status: PlanStatus,
  definitions: PlanStatusDefinition[] = [],
): string {
  if (isBuiltinPlanStatus(status)) return BUILTIN_PLAN_LABELS[status];
  return (
    definitions.find((definition) => definition.id === status)?.label ??
    "自訂狀態"
  );
}

export function planStatusShortLabel(
  status: PlanStatus,
  definitions: PlanStatusDefinition[] = [],
): string {
  if (isBuiltinPlanStatus(status)) return BUILTIN_PLAN_SHORT_LABELS[status];
  return planStatusLabel(status, definitions);
}

export function nextCustomPlanStatusID(
  definitions: PlanStatusDefinition[],
): `custom-${string}` {
  const used = new Set(definitions.map((definition) => definition.id));
  for (let index = 1; index <= 10_000; index++) {
    const candidate = `custom-${index}` as const;
    if (!used.has(candidate)) return candidate;
  }
  return `custom-${crypto.randomUUID()}`;
}

/**
 * Milestones have to stay in lifecycle order: a deadline before the start
 * date is a scheduling mistake, not a preference. Returns why a date is
 * refused, or null when it fits. Custom statuses carry no implied order.
 */
export function planOrderError(
  plans: PlanMilestone[],
  status: PlanStatus,
  date: string,
): string | null {
  if (!isBuiltinPlanStatus(status)) return null;
  const start = status === "started" ? date : plans.find((p) => p.status === "started")?.date;
  const progress =
    status === "in_progress" ? date : plans.find((p) => p.status === "in_progress")?.date;
  const done = status === "done" ? date : plans.find((p) => p.status === "done")?.date;
  if (start && progress && start > progress) {
    return "預計進行日期不可早於開始日期。";
  }
  if (progress && done && progress > done) {
    return "死線不可早於預計進行日期。";
  }
  if (start && done && start > done) {
    return "死線不可早於開始日期。";
  }
  return null;
}
