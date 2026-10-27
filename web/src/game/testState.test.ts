import { describe, expect, it } from "vitest";
import type { Run, VerifyResult } from "../api/runs";
import { makeRun } from "./testFixtures";
import { roleOfRun, testStateOf } from "./testState";

function verify(createdAt: string, result: Partial<VerifyResult>): Run {
  return makeRun({
    kind: "verify",
    status: "succeeded",
    pipelineId: "p1",
    createdAt,
    verify: { phase: "red", exitCode: 1, passed: true, durationMs: 1000, ...result },
  });
}

// W-17: red or green from the pipeline's latest verify.
describe("testStateOf", () => {
  const red = verify("2026-10-28T09:00:00Z", { phase: "red", passed: true });
  const greenOK = verify("2026-10-28T09:10:00Z", { phase: "green", exitCode: 0, passed: true });
  const greenFail = verify("2026-10-28T09:20:00Z", { phase: "green", exitCode: 1, passed: false });

  it("is red after the failing tests are proven", () => {
    expect(testStateOf("executor", [red])).toBe("red");
  });

  it("is green once the tests pass, and red again when they fail", () => {
    expect(testStateOf("executor", [red, greenOK])).toBe("green");
    expect(testStateOf("tester", [greenOK, red])).toBe("green");
    expect(testStateOf("executor", [red, greenOK, greenFail])).toBe("red");
  });

  it("shows nothing for a goal check, a missing test command, or no verify", () => {
    expect(testStateOf("executor", [verify("2026-10-28T09:00:00Z", { phase: "goal", passed: true })])).toBeUndefined();
    expect(testStateOf("executor", [verify("2026-10-28T09:00:00Z", { noCommand: true, passed: false })])).toBeUndefined();
    expect(testStateOf("executor", [makeRun({ kind: "", pipelineId: "p1" })])).toBeUndefined();
  });

  it("is only shown for the roles that test", () => {
    for (const role of ["reviewer", "planner", "researcher", undefined]) {
      expect(testStateOf(role, [red, greenOK])).toBeUndefined();
    }
  });
});

describe("roleOfRun", () => {
  it("is the record's role, or the kind's for a legacy run", () => {
    expect(roleOfRun({ role: "tester", kind: "test" })).toBe("tester");
    expect(roleOfRun({ kind: "review" })).toBe("reviewer");
    expect(roleOfRun({ kind: "" })).toBe("executor");
    expect(roleOfRun({ kind: "revise" })).toBe("executor");
    expect(roleOfRun({ kind: "plan" })).toBe("planner");
    expect(roleOfRun({ kind: "judge" })).toBe("qa");
    expect(roleOfRun({ kind: "verify" })).toBeUndefined();
  });
});
