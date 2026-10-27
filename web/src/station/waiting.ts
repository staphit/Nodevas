// What Station's "waiting on you" strip adds to the agent panel's asks and
// held mail: runs that need a person. Plain data, tested without React.

import type { Run } from "../api";

/** Older than this is not waiting any more, just history. */
export const WAITING_WINDOW_MS = 6 * 60 * 60 * 1000;

export type WaitingRunKind = "limit" | "merge";

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
 * - merge: succeeded with an open PR whose CI passed and no merge under way.
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
    const finished = millis(run.finishedAt);
    if (!Number.isFinite(finished) || now - finished > windowMs) continue;
    if (run.status === "failed" && (run.failureKind === "rate_limit" || run.failureKind === "quota")) {
      const key = `limit:${run.nodeId}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ kind: "limit", run, tookOver: byRetry.get(run.id) });
      continue;
    }
    const merging = run.merge && (run.merge.status === "pending" || run.merge.status === "merged");
    if (run.status === "succeeded" && run.prUrl && run.prState === "open" && run.ciStatus === "success" && !merging) {
      const key = `merge:${run.nodeId}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ kind: "merge", run });
    }
  }
  return out;
}
