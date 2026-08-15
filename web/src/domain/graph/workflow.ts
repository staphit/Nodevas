/**
 * Workflow definition reducers [A-04].
 *
 * Owns `graph.yaml → ui.customStatuses` (actual lifecycle states) and
 * `ui.planStatuses` (expected milestone types). Project Settings is the only
 * screen allowed to call these.
 *
 * Deleting a definition never rewrites history: existing journal events and
 * scheduled milestones keep their id, and the UI falls back to a neutral label.
 * Use `../usage` first to show what a deletion would affect.
 */

import type { Graph, PlanStatusDefinition, StatusDefinition } from "../../types";
import { invalid, notFound } from "../errors";
import type { CustomLifecycleStatus, MilestoneType } from "../glossary";

const SHAPES: StatusDefinition["shape"][] = [
  "circle",
  "square",
  "diamond",
  "triangle",
  "dash",
];

export function lifecycleStatusDefinitions(graph: Graph): StatusDefinition[] {
  return graph.ui?.customStatuses ?? [];
}

export function milestoneTypeDefinitions(graph: Graph): PlanStatusDefinition[] {
  return graph.ui?.planStatuses ?? [];
}

export function nextCustomLifecycleStatusID(
  definitions: StatusDefinition[],
): CustomLifecycleStatus {
  const used = new Set(definitions.map((definition) => definition.id));
  for (let index = 1; index <= 10_000; index++) {
    const candidate = `custom-status-${index}` as const;
    if (!used.has(candidate)) return candidate;
  }
  return `custom-status-${crypto.randomUUID()}`;
}

export function nextMilestoneTypeID(
  definitions: PlanStatusDefinition[],
): `custom-${string}` {
  const used = new Set(definitions.map((definition) => definition.id));
  for (let index = 1; index <= 10_000; index++) {
    const candidate = `custom-${index}` as const;
    if (!used.has(candidate)) return candidate;
  }
  return `custom-${crypto.randomUUID()}`;
}

function assertLabel(label: string, existing: { label: string; id: string }[], id?: string) {
  const trimmed = label.trim();
  if (!trimmed) throw invalid("名稱不可空白。");
  const clash = existing.some(
    (definition) =>
      definition.id !== id &&
      definition.label.localeCompare(trimmed, undefined, { sensitivity: "accent" }) === 0,
  );
  if (clash) throw invalid(`已存在名稱「${trimmed}」。`);
  return trimmed;
}

// --- actual lifecycle states -----------------------------------------------

export function addLifecycleStatus(
  graph: Graph,
  definition: Omit<StatusDefinition, "id"> & { id?: CustomLifecycleStatus },
): CustomLifecycleStatus {
  graph.ui = graph.ui ?? {};
  const current = graph.ui.customStatuses ?? [];
  const label = assertLabel(definition.label, current);
  if (!SHAPES.includes(definition.shape)) throw invalid("不支援的圖形。");
  const id = definition.id ?? nextCustomLifecycleStatusID(current);
  if (current.some((item) => item.id === id)) {
    throw invalid(`狀態代號「${id}」已存在。`);
  }
  graph.ui.customStatuses = [
    ...current,
    {
      id,
      label,
      color: definition.color,
      shape: definition.shape,
      ...(definition.settled ? { settled: true } : {}),
    },
  ];
  return id;
}

export function updateLifecycleStatus(
  graph: Graph,
  id: CustomLifecycleStatus,
  patch: Partial<Omit<StatusDefinition, "id">>,
): void {
  const current = graph.ui?.customStatuses ?? [];
  const target = current.find((definition) => definition.id === id);
  if (!target) throw notFound(`狀態「${id}」`);
  if (patch.label !== undefined) target.label = assertLabel(patch.label, current, id);
  if (patch.color !== undefined) target.color = patch.color;
  if (patch.shape !== undefined) {
    if (!SHAPES.includes(patch.shape)) throw invalid("不支援的圖形。");
    target.shape = patch.shape;
  }
  if (patch.settled !== undefined) {
    // Kept off the record when false so the file stays as it was before this
    // property existed.
    if (patch.settled) target.settled = true;
    else delete target.settled;
  }
}

/** Removes the definition only. Journal events keep their status id. */
export function removeLifecycleStatus(graph: Graph, id: CustomLifecycleStatus): void {
  if (!graph.ui?.customStatuses) return;
  graph.ui.customStatuses = graph.ui.customStatuses.filter(
    (definition) => definition.id !== id,
  );
}

// --- expected milestone types ----------------------------------------------

export function addMilestoneType(
  graph: Graph,
  definition: { label: string; id?: `custom-${string}` },
): `custom-${string}` {
  graph.ui = graph.ui ?? {};
  const current = graph.ui.planStatuses ?? [];
  const label = assertLabel(definition.label, current);
  const id = definition.id ?? nextMilestoneTypeID(current);
  if (current.some((item) => item.id === id)) {
    throw invalid(`里程碑代號「${id}」已存在。`);
  }
  graph.ui.planStatuses = [...current, { id, label }];
  return id;
}

export function updateMilestoneType(
  graph: Graph,
  id: `custom-${string}`,
  patch: { label?: string },
): void {
  const current = graph.ui?.planStatuses ?? [];
  const target = current.find((definition) => definition.id === id);
  if (!target) throw notFound(`里程碑類型「${id}」`);
  if (patch.label !== undefined) target.label = assertLabel(patch.label, current, id);
}

/**
 * Removes the type definition. Scheduled milestones of that type are dropped
 * only when `removeScheduled` is set — otherwise they stay and render with a
 * fallback label, so nothing silently disappears from a plan.
 */
export function removeMilestoneType(
  graph: Graph,
  id: MilestoneType,
  options: { removeScheduled?: boolean } = {},
): void {
  if (graph.ui?.planStatuses) {
    graph.ui.planStatuses = graph.ui.planStatuses.filter(
      (definition) => definition.id !== id,
    );
  }
  if (!options.removeScheduled || !graph.ui?.plans) return;
  for (const [nodeId, plans] of Object.entries(graph.ui.plans)) {
    const remaining = plans.filter((plan) => plan.status !== id);
    if (remaining.length) graph.ui.plans[nodeId] = remaining;
    else delete graph.ui.plans[nodeId];
  }
}
