// Workflow stage wording shared by Settings, a Goal's override, the dispatch
// dialog and the autopilot panel.

import { costHint, estimateCost } from "../domain/graph/stages";
import type { Workflow, WorkflowPreset } from "../types";

type Translate = (key: string, values?: Record<string, string | number>) => string;

export function presetName(preset: WorkflowPreset | string, t: Translate): string {
  return t(`stages.preset.${preset}`);
}

/** "≈1.8×": a workflow's own expected cost relative to Light. */
export function exactCost(workflow: Workflow | null | undefined): string {
  const cost = estimateCost(workflow);
  const rounded = Math.round(cost * 10) / 10;
  return rounded === 1 ? "1×" : `≈${rounded.toFixed(1)}×`;
}

/** "Standard · ≈2–3×", "Legacy": a settings row's value. */
export function workflowSummary(workflow: Workflow | null | undefined, t: Translate): string {
  if (!workflow) return t("stages.legacy");
  return `${presetName(workflow.preset, t)} · ${costHint(estimateCost(workflow))}`;
}
