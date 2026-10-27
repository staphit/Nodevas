// What Station's "Waiting on you" card lists: everything the hub knows waits
// on a person. Questions and held mail from the agent network, autopilot
// tasks held for a person's review, runs that need one (a usage limit, a PR
// ready to merge, a PR whose CI went red, an offer the runner has not taken
// yet) and a spent budget. Plain data, tested without React.

import type { AgentAsk, AgentMessage, Run, RunBudgetLimit } from "../api";
import type { WaitingTask } from "../autopilot/autopilotStore";

/** Older than this is not waiting any more, just history. */
export const WAITING_WINDOW_MS = 6 * 60 * 60 * 1000;

export type WaitingRunKind = "limit" | "merge" | "ci";

export interface WaitingRun {
  kind: WaitingRunKind;
  run: Run;
  /** A limit run: the fallback run that took over, if one did. */
  tookOver?: Run;
}

function millis(value: string | undefined): number {
  return value ? Date.parse(value) : NaN;
}

/**
 * The runs worth a person's attention, newest first, one per node and kind:
 * - limit: failed on its agent's usage limit (rate_limit or quota), with the
 *   run that took over named when there is one;
 * - merge: succeeded with an open PR whose CI passed and no merge under way;
 * - ci: succeeded with an open PR whose CI failed.
 * A node's PR is listed once, as its newest run has it: an older run's green
 * PR is not "ready to merge" while a newer run is still going or came back
 * without one (pending CI, a failure).
 */
export function waitingRuns(runs: Run[], now: number, windowMs = WAITING_WINDOW_MS): WaitingRun[] {
  const byRetry = new Map<string, Run>();
  for (const run of runs) if (run.retryOf) byRetry.set(run.retryOf, run);
  const seen = new Set<string>();
  const out: WaitingRun[] = [];
  const sorted = [...runs].sort(
    (a, b) => (millis(b.finishedAt) || millis(b.createdAt)) - (millis(a.finishedAt) || millis(a.createdAt)),
  );
  for (const run of sorted) {
    // The node's newest run claims its PR line before anything else is asked
    // of it, whether or not it has a PR worth listing.
    const prKey = `pr:${run.nodeId}`;
    const newest = !seen.has(prKey);
    seen.add(prKey);
    const finished = millis(run.finishedAt);
    if (!Number.isFinite(finished) || now - finished > windowMs) continue;
    if (run.status === "failed" && (run.failureKind === "rate_limit" || run.failureKind === "quota")) {
      const key = `limit:${run.nodeId}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ kind: "limit", run, tookOver: byRetry.get(run.id) });
      continue;
    }
    if (!newest || run.status !== "succeeded" || !run.prUrl || run.prState !== "open") continue;
    const merging = run.merge && (run.merge.status === "pending" || run.merge.status === "merged");
    const kind = run.ciStatus === "success" && !merging ? "merge" : run.ciStatus === "failure" ? "ci" : null;
    if (!kind) continue;
    out.push({ kind, run });
  }
  return out;
}

export type WaitingItem =
  | { kind: "budget"; limit: RunBudgetLimit | string }
  | { kind: "ask"; ask: AgentAsk }
  | { kind: "held"; message: AgentMessage }
  | { kind: "review"; task: WaitingTask }
  | { kind: "offer"; run: Run }
  | WaitingRun;

export interface WaitingInput {
  /** The project's recent runs (Station's store). */
  runs: Run[];
  /** The runs not ended yet: offers come from here. */
  active: Run[];
  asks: AgentAsk[];
  /** Held messages the viewer may decide (the store keeps only those). */
  held: AgentMessage[];
  /** Autopilot tasks waiting for a person. */
  reviews: WaitingTask[];
  /** The budget limit that blocks new runs, or "". */
  budgetExceeded?: RunBudgetLimit | string;
  /** Whether the viewer may answer an ask (its session's person, or an admin). */
  canAnswer: (ask: AgentAsk) => boolean;
  now: number;
}

/** A stable key per item, for lists and tests. */
export function waitingKey(item: WaitingItem): string {
  switch (item.kind) {
    case "budget":
      return "budget";
    case "ask":
      return `ask:${item.ask.id}`;
    case "held":
      return `held:${item.message.id}`;
    case "review":
      return `review:${item.task.autopilotId}:${item.task.task.nodeId}`;
    default:
      return `${item.kind}:${item.run.id}`;
  }
}

/**
 * Everything waiting on the viewer, most blocking first: a spent budget
 * (nothing new runs), questions an agent is stopped on, held mail, reviews an
 * autopilot is stopped on, PRs to merge or fix, offers not yet taken, and
 * usage limits last (a fallback usually took over).
 */
export function waitingOnYou(input: WaitingInput): WaitingItem[] {
  const out: WaitingItem[] = [];
  if (input.budgetExceeded) out.push({ kind: "budget", limit: input.budgetExceeded });
  for (const ask of input.asks) {
    if (ask.status === "pending" && input.canAnswer(ask)) out.push({ kind: "ask", ask });
  }
  for (const message of input.held) out.push({ kind: "held", message });
  for (const task of input.reviews) out.push({ kind: "review", task });
  const runs = waitingRuns(input.runs, input.now);
  for (const item of runs) if (item.kind === "merge") out.push(item);
  for (const item of runs) if (item.kind === "ci") out.push(item);
  for (const run of input.active) if (run.status === "offered") out.push({ kind: "offer", run });
  for (const item of runs) if (item.kind === "limit") out.push(item);
  return out;
}
