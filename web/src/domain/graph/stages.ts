// Workflow stages: which stages of the task and goal flows a project runs,
// their presets and their expected token cost. Mirrors
// internal/engine/workflow.go; pinned by testdata/workflow_presets.json.
//
// RED PHASE STUB: declarations only, no behavior.

import type { Graph, Workflow, WorkflowPreset, WorkflowStages } from "../../types";

export const PRESET_ORDER: readonly WorkflowPreset[] = [];

export const PRESETS: Readonly<Partial<Record<WorkflowPreset, WorkflowStages>>> = {};

export const DEFAULT_TEST_GLOBS: readonly string[] = [];

export const COST_WEIGHTS: Readonly<Record<string, number>> = {};

export function workflowForPreset(_preset: WorkflowPreset): Workflow | null {
  return null;
}

/** The preset a stage set is, or "custom". */
export function presetOf(_stages: WorkflowStages): WorkflowPreset {
  return "custom";
}

/** What a new project of this layout gets. */
export function newProjectWorkflow(_layout: string | undefined): Workflow | null {
  return null;
}

/** The goal's override, else the project's, else null (legacy). */
export function effectiveWorkflow(_graph: Graph | null | undefined, _goalId?: string): Workflow | null {
  return null;
}

/** The goal a node belongs to (its goal:<id> tag), or "". */
export function goalOf(_graph: Graph | null | undefined, _nodeId: string): string {
  return "";
}

export function requiresAcceptance(_workflow: Workflow | null | undefined): boolean {
  return false;
}

/** Expected token cost relative to Light (one executor run = 1). */
export function estimateCost(_workflow: Workflow | null | undefined): number {
  return 0;
}

/** "1×", "≈2–3×" or "≈3–4×". */
export function costHint(_cost: number): string {
  return "";
}

/** Throws with every problem in a workflow's levers. */
export function validateWorkflow(_workflow: Workflow): void {}
