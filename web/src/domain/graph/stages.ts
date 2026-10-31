// Workflow stages: which stages of the task and goal flows a project runs,
// their presets and their expected token cost. Mirrors
// internal/engine/workflow.go; pinned by testdata/workflow_presets.json.
//
// Owns `graph.yaml → workflow` (and a Goal's `nodes[].workflow` override).
// Absent or null is legacy: today's behavior, configured per autopilot.

import type { Graph, Workflow, WorkflowPreset, WorkflowStages } from "../../types";

export const PRESET_ORDER: readonly WorkflowPreset[] = Object.freeze(["light", "standard", "full", "custom"] satisfies WorkflowPreset[]);

export const STAGE_KEYS: readonly (keyof WorkflowStages)[] = Object.freeze([
  "planner",
  "goalQa",
  "tester",
  "reviewer",
  "ci",
  "goalReview",
] satisfies (keyof WorkflowStages)[]);

export const PRESETS: Readonly<Partial<Record<WorkflowPreset, WorkflowStages>>> = Object.freeze({
  light: { planner: true, goalQa: false, tester: false, reviewer: false, ci: false, goalReview: false },
  standard: { planner: true, goalQa: false, tester: true, reviewer: true, ci: false, goalReview: false },
  full: { planner: true, goalQa: true, tester: true, reviewer: true, ci: true, goalReview: true },
});

export const DEFAULT_TEST_GLOBS: readonly string[] = Object.freeze([
  "**/*_test.go",
  "**/*.test.ts",
  "**/*.test.tsx",
  "**/*.spec.ts",
  "**/*.spec.tsx",
  "**/test/**",
  "**/tests/**",
  "**/__tests__/**",
  "e2e/**",
  "**/test_*.py",
  "**/*_test.py",
]);

/** Lever defaults and bounds (inclusive). */
export const WORKFLOW_DEFAULTS = Object.freeze({ maxQaRounds: 2, maxTesterRounds: 2, maxGreenTries: 3 });
export const WORKFLOW_BOUNDS = Object.freeze({
  reviewerSkipBelowLines: [0, 10000] as const,
  maxQaRounds: [1, 5] as const,
  maxTesterRounds: [1, 3] as const,
  maxGreenTries: [1, 5] as const,
  testGlobs: 32,
});

/** One executor run = 1. */
export const COST_WEIGHTS: Readonly<Record<string, number>> = Object.freeze({
  executor: 1.0,
  tester: 0.5,
  verify: 0,
  reviewer: 0.5,
  revise: 0.3,
  ci: 0,
  ciFix: 0.3,
  goalQa: 0.5,
  storyJudge: 0.3,
  planner: 1.0,
  smallDiffShare: 0.3,
});

const HINT_BANDS: readonly { max: number; label: string }[] = [
  { max: 1.5, label: "1×" },
  { max: 3.0, label: "≈2–3×" },
  { max: 99, label: "≈3–4×" },
];

export function workflowForPreset(preset: WorkflowPreset): Workflow | null {
  const stages = PRESETS[preset] ?? PRESETS.standard!;
  return { preset, stages: { ...stages }, judge: "" };
}

/** The preset a stage set is, or "custom". */
export function presetOf(stages: WorkflowStages): WorkflowPreset {
  for (const preset of PRESET_ORDER) {
    const candidate = PRESETS[preset];
    if (candidate && STAGE_KEYS.every((key) => Boolean(stages[key]) === candidate[key])) return preset;
  }
  return "custom";
}

/** What a new project of this layout gets. */
export function newProjectWorkflow(layout: string | undefined): Workflow | null {
  const workflow = workflowForPreset("standard")!;
  workflow.judge = layout === "story" ? "checklist" : "";
  return workflow;
}

function nodeById(graph: Graph | null | undefined, id: string) {
  return (graph?.nodes ?? []).find((node) => node.id === id);
}

/** The goal's override, else the project's, else null (legacy). */
export function effectiveWorkflow(graph: Graph | null | undefined, goalId?: string): Workflow | null {
  if (goalId) {
    const override = nodeById(graph, goalId)?.workflow;
    if (override) return override;
  }
  return graph?.workflow ?? null;
}

/** The goal a node belongs to (its goal:<id> tag), or "". */
export function goalOf(graph: Graph | null | undefined, nodeId: string): string {
  for (const tag of nodeById(graph, nodeId)?.tags ?? []) {
    if (!tag.startsWith("goal:")) continue;
    const id = tag.slice("goal:".length);
    if (id && nodeById(graph, id)) return id;
  }
  return "";
}

/** The workflow a node is worked under: its goal's override or its project's. */
export function workflowForNode(graph: Graph | null | undefined, nodeId: string): Workflow | null {
  const own = nodeById(graph, nodeId)?.workflow;
  if (own) return own;
  return effectiveWorkflow(graph, goalOf(graph, nodeId));
}

export function requiresAcceptance(workflow: Workflow | null | undefined): boolean {
  if (!workflow) return false;
  return Boolean(workflow.stages?.tester || workflow.stages?.reviewer || workflow.judge === "checklist");
}

/** Expected token cost relative to Light (one executor run = 1). */
export function estimateCost(workflow: Workflow | null | undefined): number {
  const w = COST_WEIGHTS;
  if (!workflow) return w.executor;
  const stages = workflow.stages ?? ({} as Partial<WorkflowStages>);
  let cost = w.executor;
  if (stages.tester) cost += workflow.judge === "checklist" ? w.storyJudge : w.tester;
  if (stages.reviewer) {
    cost += w.reviewer - ((workflow.reviewerSkipBelowLines ?? 0) > 0 ? w.reviewer * w.smallDiffShare : 0);
  }
  if (stages.tester || stages.reviewer) cost += w.revise;
  if (stages.goalQa) cost += w.goalQa;
  if (stages.ci) cost += w.ciFix;
  return cost;
}

/** "1×", "≈2–3×" or "≈3–4×". */
export function costHint(cost: number): string {
  return (HINT_BANDS.find((band) => cost <= band.max) ?? HINT_BANDS[HINT_BANDS.length - 1]).label;
}

function checkBound(problems: string[], name: string, value: number | undefined, [min, max]: readonly [number, number]) {
  if (value === undefined) return;
  if (!Number.isInteger(value) || value < min || value > max) problems.push(`${name} must be ${min}–${max}`);
}

/** Throws with every problem in a workflow's levers. */
export function validateWorkflow(workflow: Workflow): void {
  const problems: string[] = [];
  if (!PRESET_ORDER.includes(workflow.preset)) problems.push(`unknown preset ${String(workflow.preset)}`);
  if (workflow.preset !== "custom" && PRESETS[workflow.preset] && presetOf(workflow.stages) !== workflow.preset) {
    problems.push(`the ${workflow.preset} preset's stages were changed (use custom)`);
  }
  // reviewerSkipBelowLines: 0 = never skip.
  if (workflow.reviewerSkipBelowLines !== undefined) {
    const [min, max] = WORKFLOW_BOUNDS.reviewerSkipBelowLines;
    const value = workflow.reviewerSkipBelowLines;
    if (!Number.isInteger(value) || value < min || value > max) problems.push(`reviewer skip threshold must be ${min}–${max} lines`);
  }
  checkBound(problems, "max QA rounds", workflow.maxQaRounds, WORKFLOW_BOUNDS.maxQaRounds);
  checkBound(problems, "max tester rounds", workflow.maxTesterRounds, WORKFLOW_BOUNDS.maxTesterRounds);
  checkBound(problems, "max green tries", workflow.maxGreenTries, WORKFLOW_BOUNDS.maxGreenTries);
  if ((workflow.testGlobs?.length ?? 0) > WORKFLOW_BOUNDS.testGlobs) {
    problems.push(`at most ${WORKFLOW_BOUNDS.testGlobs} test globs`);
  }
  if (workflow.judge !== undefined && workflow.judge !== "" && workflow.judge !== "checklist") {
    problems.push(`unknown judge ${String(workflow.judge)}`);
  }
  if (problems.length) throw new Error(problems.join("; "));
}
