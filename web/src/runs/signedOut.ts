// An agent CLI that is on its machine but not signed in (runnerwire
// FailureSignedOut, Agent.signedOut). Signing in happens on that machine, so
// who reads decides the words: on the single-user edition's own runner, the
// person at this computer, in plain words; on a team hub, the runner's owner
// gets the command to run there, anyone else whom to ask.

import type { Actor, Run, Runner } from "../api";
import { agentName } from "../game/labels";
import { runnerDisplayName } from "../runners/runnerName";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** RunResult.FailureCode of a run whose agent was signed out. */
export const SIGNED_OUT = "signed_out";

/** What signs kind's CLI in, typed in a terminal on its machine: Claude
 * Code and the Gemini CLI start their sign-in when run, the others have a
 * command for it. */
export function signInCommand(kind: string): string {
  switch (kind) {
    case "codex":
      return "codex login";
    case "opencode":
      return "opencode auth login";
    case "antigravity":
      return "agy";
    default:
      return kind;
  }
}

/** Whether a run failed because its agent was signed out. */
export function runSignedOut(run: Pick<Run, "status" | "failureCode">): boolean {
  return run.status === "failed" && run.failureCode === SIGNED_OUT;
}

/** Who reads about a sign-in on the run's runner: "local" for this
 * computer's built-in runner (single-user edition), "owner" for the runner's
 * owner (anyone on a server without accounts), "other" for everyone else. */
export type SignInReader = "local" | "owner" | "other";

export function signInReader(
  run: Pick<Run, "runnerId">,
  runners: readonly Runner[],
  viewer: Actor | null,
): SignInReader {
  const runner = runners.find((item) => item.id === run.runnerId);
  if (runner?.builtIn) return "local";
  if (runner && (!viewer || runner.ownerId === viewer.id)) return "owner";
  return "other";
}

/** The sentence that says the agent is signed out and what to do. */
export function signedOutText(t: Translate, run: Pick<Run, "agent" | "runnerName" | "runnerId">, reader: SignInReader): string {
  const agent = agentName(run.agent);
  const command = signInCommand(run.agent);
  if (reader === "local") return t("result.signedOut", { agent, command });
  const machine = runnerDisplayName(t, run.runnerName) || run.runnerId;
  return t(reader === "owner" ? "runs.signedOut.owner" : "runs.signedOut.other", { agent, machine, command });
}
