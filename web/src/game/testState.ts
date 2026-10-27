import type { Run } from "../api/runs";

/** Red or green: the latest verify run of a pipeline, as a Tester's or an
 * Executor's desk shows it. undefined when there is none to show (no verify
 * yet, no test command, a goal verification, or a role that does not test). */
export type TestState = "red" | "green";

/** The roles whose desks show the test state. */
export const TEST_STATE_ROLES: readonly string[] = ["tester", "executor"];

/** The test state of a pipeline's latest verify run, for an actor in role. */
export function testStateOf(_role: string | undefined, _pipelineRuns: readonly Run[]): TestState | undefined {
  // TODO(green): implement.
  return undefined;
}

/** A run's role: its record's, or its kind's for a legacy run. */
export function roleOfRun(_run: Pick<Run, "role" | "kind">): string | undefined {
  // TODO(green): implement.
  return undefined;
}
