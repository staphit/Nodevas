import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { Graph, Workflow, WorkflowStages } from "../../types";
import {
  costHint,
  DEFAULT_TEST_GLOBS,
  effectiveWorkflow,
  estimateCost,
  goalOf,
  newProjectWorkflow,
  PRESET_ORDER,
  PRESETS,
  presetOf,
  requiresAcceptance,
  validateWorkflow,
  workflowForPreset,
} from "./stages";

const fixturePath = [
  resolve(process.cwd(), "testdata/workflow_presets.json"),
  resolve(process.cwd(), "../testdata/workflow_presets.json"),
].find(existsSync);
if (!fixturePath) throw new Error("cannot locate testdata/workflow_presets.json");
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as {
  presets: Record<string, WorkflowStages>;
  presetOrder: string[];
  defaults: { testGlobs: string[] };
  expectedCost: Record<string, number>;
  hintBands: { max: number; label: string }[];
};

const custom = (stages: Partial<WorkflowStages>): Workflow => ({
  preset: "custom",
  stages: { planner: false, goalQa: false, tester: false, reviewer: false, ci: false, goalReview: false, ...stages },
});

describe("presets", () => {
  it("match the shared fixture", () => {
    expect(PRESETS).toEqual(fixture.presets);
    expect([...PRESET_ORDER]).toEqual(fixture.presetOrder);
    expect([...DEFAULT_TEST_GLOBS]).toEqual(fixture.defaults.testGlobs);
    expect(workflowForPreset("standard")).toMatchObject({ preset: "standard", stages: fixture.presets.standard });
    expect(presetOf(fixture.presets.full)).toBe("full");
    expect(presetOf({ ...fixture.presets.full, ci: false })).toBe("custom");
  });

  it("give new projects Standard, and story projects checklist judging", () => {
    expect(newProjectWorkflow("workflow")).toMatchObject({ preset: "standard", judge: "" });
    expect(newProjectWorkflow("story")).toMatchObject({ preset: "standard", judge: "checklist" });
  });
});

describe("effective workflow", () => {
  const goal = (workflow?: Workflow): Graph =>
    ({
      version: 1,
      workflow: workflowForPreset("light"),
      nodes: [
        { id: "g", kind: "goal", workflow },
        { id: "t", tags: ["x", "goal:g"] },
        { id: "loose" },
      ],
    }) as Graph;

  it("is legacy without a workflow", () => {
    expect(effectiveWorkflow({ version: 1, nodes: [] } as Graph)).toBeNull();
    expect(effectiveWorkflow({ version: 1, nodes: [], workflow: null } as Graph)).toBeNull();
    expect(requiresAcceptance(null)).toBe(false);
  });

  it("lets a goal override its project", () => {
    const full = workflowForPreset("full")!;
    expect(goalOf(goal(full), "t")).toBe("g");
    expect(goalOf(goal(full), "loose")).toBe("");
    expect(effectiveWorkflow(goal(full), "g")?.preset).toBe("full");
    expect(effectiveWorkflow(goal(), "g")?.preset).toBe("light");
  });

  it("requires acceptance when tests, review or checklists judge the work", () => {
    expect(requiresAcceptance(workflowForPreset("light"))).toBe(false);
    expect(requiresAcceptance(workflowForPreset("standard"))).toBe(true);
    expect(requiresAcceptance({ ...workflowForPreset("light")!, judge: "checklist" })).toBe(true);
  });
});

describe("cost", () => {
  it("is 1× for Light, ≈2–3× for Standard and ≈3–4× for Full", () => {
    for (const preset of ["light", "standard", "full"] as const) {
      expect(estimateCost(workflowForPreset(preset))).toBeCloseTo(fixture.expectedCost[preset], 5);
    }
    expect(costHint(estimateCost(workflowForPreset("light")))).toBe("1×");
    expect(costHint(estimateCost(workflowForPreset("standard")))).toBe("≈2–3×");
    expect(costHint(estimateCost(workflowForPreset("full")))).toBe("≈3–4×");
  });

  it("adds each custom stage's weight", () => {
    expect(estimateCost(custom({}))).toBeCloseTo(1, 5);
    expect(estimateCost(custom({ tester: true }))).toBeCloseTo(1.8, 5);
    expect(estimateCost(custom({ reviewer: true }))).toBeCloseTo(1.8, 5);
    // The reviewer-skip lever takes off half a review for the small share.
    const skip = { ...workflowForPreset("standard")!, reviewerSkipBelowLines: 50 };
    expect(estimateCost(skip)).toBeCloseTo(2.3 - 0.5 * 0.3, 5);
    // Story judging costs a judge pass instead of a tester.
    expect(estimateCost({ ...workflowForPreset("standard")!, judge: "checklist" })).toBeCloseTo(2.1, 5);
  });

  it("validates levers like the server", () => {
    expect(() => validateWorkflow(workflowForPreset("standard")!)).not.toThrow();
    expect(() => validateWorkflow({ ...workflowForPreset("standard")!, maxQaRounds: 6 })).toThrow(/QA rounds/);
    expect(() => validateWorkflow({ ...workflowForPreset("standard")!, reviewerSkipBelowLines: 10001 })).toThrow();
  });
});
