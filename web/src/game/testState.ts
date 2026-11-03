import type { Run } from "../api/runs";

/** Red or green: the latest verify run of a pipeline, as a Tester's or an
 * Executor's desk shows it. undefined when there is none to show (no verify
 * yet, no test command, a goal verification, or a role that does not test). */
export type TestState = "red" | "green";

/** The roles whose desks show the test state. */
export const TEST_STATE_ROLES: readonly string[] = ["tester", "executor"];

const time = (iso: string | undefined) => {
  const at = Date.parse(iso ?? "");
  return Number.isFinite(at) ? at : 0;
};

/** The test state of a pipeline's latest verify run, for an actor in role. */
export function testStateOf(role: string | undefined, pipelineRuns: readonly Run[]): TestState | undefined {
  if (!role || !TEST_STATE_ROLES.includes(role)) return undefined;
  let latest: Run | undefined;
  for (const run of pipelineRuns) {
    if (run.kind !== "verify" || !run.verify) continue;
    if (!latest || time(run.createdAt) > time(latest.createdAt)) latest = run;
  }
  const verify = latest?.verify;
  if (!verify || verify.noCommand) return undefined;
  if (verify.phase === "green") return verify.passed ? "green" : "red";
  // The red phase passes when the new tests fail, as they should first.
  if (verify.phase === "red") return verify.passed ? "red" : undefined;
  return undefined;
}

/** The role a legacy run (no recorded role) acted in, by its kind. */
const ROLE_BY_KIND: Readonly<Record<string, string>> = {
  "": "executor",
  implement: "executor",
  revise: "executor",
  review: "reviewer",
  plan: "planner",
  test: "tester",
  judge: "qa",
};

/** A run's role: its record's, or its kind's for a legacy run. */
export function roleOfRun(run: Pick<Run, "role" | "kind">): string | undefined {
  if (run.role) return run.role;
  return ROLE_BY_KIND[run.kind ?? ""];
}
