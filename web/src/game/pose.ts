// What a character is doing. One ordered decision per kind of character, so
// a run that is several things at once (failed CI on a PR that also waits
// for a merge) always shows the same, most urgent one:
//
//   offline > asking-user > failed > limit > awaiting-accept > waiting-ci
//           > reviewing > working > finished > idle

import type { AgentSession } from "../api/agents";
import { runFinal, runWaitsForLimit, type Run } from "../api/runs";
import { sessionOnline } from "./presence";
import type { Pose } from "./world";

export interface RunPoseContext {
  /** The runner's online state; null when the runner is not known (it is
   * then never the reason for "offline"). */
  runnerOnline: boolean | null;
  /** The run's agent has a pending ask_user. */
  asking: boolean;
}

/** Whether the run's PR is open with CI in the given state. */
function openPRWithCI(run: Run, ci: string): boolean {
  return Boolean(run.prUrl) && run.prState === "open" && run.ciStatus === ci;
}

export function runPose(run: Run, ctx: RunPoseContext): Pose {
  // A run that has not ended is stuck while its runner is away: queued work
  // is not offered, a running agent's runner cannot report.
  if (!runFinal(run.status) && ctx.runnerOnline === false) return "offline";
  if (ctx.asking) return "asking-user";
  if (run.status === "failed" || openPRWithCI(run, "failure")) return "failed";
  if (runWaitsForLimit(run)) return "limit";
  if (run.status === "queued" || run.status === "offered" || run.merge?.status === "pending") {
    return "awaiting-accept";
  }
  if (openPRWithCI(run, "pending")) return "waiting-ci";
  if (run.status === "running") return run.kind === "review" ? "reviewing" : "working";
  if (run.status === "succeeded") return "finished";
  return "idle";
}

export interface SessionPoseContext {
  now: number;
  asking: boolean;
}

export function sessionPose(
  session: Pick<AgentSession, "online" | "lastSeen" | "state">,
  ctx: SessionPoseContext,
): Pose {
  if (!sessionOnline(session, ctx.now)) return "offline";
  if (ctx.asking) return "asking-user";
  return session.state === "busy" ? "working" : "idle";
}

/** Whether a run in a final state still deserves a character for long: it
 * needs someone (failed, red or pending CI, a merge in flight). A cleanly
 * finished run only cheers for a moment (office.FINISHED_SHOW_MS). */
export function finishedRunStaysInRoom(run: Run): boolean {
  if (!runFinal(run.status)) return true;
  if (run.status === "failed") return true;
  if (run.merge?.status === "pending") return true;
  return openPRWithCI(run, "failure") || openPRWithCI(run, "pending");
}
