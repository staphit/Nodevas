// Station's control center, the parts that are plain data: which runs to
// list and which autopilot each belongs to, how long a run has gone, and
// what "Pause everything" is about to do. StationControl draws them.

import { autopilotEnded, runFinal, type Actor, type AutopilotView, type Run } from "../api";

/** Ended runs stay listed (folded away) this long after they finish. */
export const RECENT_ENDED_MS = 60 * 60 * 1000;
/** And at most this many of them. */
export const MAX_ENDED_ROWS = 10;

export type ControlRunKind = "implement" | "review" | "revise" | "plan";

export function controlRunKind(run: Pick<Run, "kind">): ControlRunKind {
  if (run.kind === "review" || run.kind === "revise" || run.kind === "plan") return run.kind;
  return "implement";
}

export interface ControlRow {
  run: Run;
  /** The autopilot whose task this run is a step of, if any. */
  autopilot?: AutopilotView;
}

/** Each run id and pipeline id an autopilot's tasks name, to its autopilot. A
 * task's review and revise runs share its pipeline, not its run id. When two
 * autopilots name the same one (a goal run again), the live one wins. */
export function autopilotIndex(views: Record<string, AutopilotView>): Map<string, AutopilotView> {
  const index = new Map<string, AutopilotView>();
  const put = (key: string | undefined, view: AutopilotView) => {
    if (!key) return;
    const held = index.get(key);
    if (held && !autopilotEnded(held.run.status) && autopilotEnded(view.run.status)) return;
    index.set(key, view);
  };
  for (const view of Object.values(views)) {
    for (const task of view.tasks ?? []) {
      put(task.runId && `run:${task.runId}`, view);
      put(task.pipelineId && `pipeline:${task.pipelineId}`, view);
    }
  }
  return index;
}

export function autopilotOf(index: Map<string, AutopilotView>, run: Run): AutopilotView | undefined {
  return index.get(`run:${run.id}`) ?? (run.pipelineId ? index.get(`pipeline:${run.pipelineId}`) : undefined);
}

function time(value: string | undefined): number {
  const at = value ? Date.parse(value) : NaN;
  return Number.isFinite(at) ? at : 0;
}

/**
 * The rows: every run that has not ended (offered and queued first, then
 * running, newest first in each), and the runs that ended in the last hour,
 * newest first. `active` is the authority on what is live; `recent` (the
 * project's newest runs) supplies the ended ones.
 */
export function controlRows(
  active: Run[],
  recent: Run[],
  views: Record<string, AutopilotView>,
  now: number,
): { live: ControlRow[]; ended: ControlRow[] } {
  const index = autopilotIndex(views);
  const row = (run: Run): ControlRow => ({ run, autopilot: autopilotOf(index, run) });
  const liveIds = new Set(active.map((run) => run.id));
  const live = active
    .filter((run) => !runFinal(run.status))
    .sort((a, b) => {
      const pa = a.status === "running" ? 1 : 0;
      const pb = b.status === "running" ? 1 : 0;
      return pa - pb || time(b.createdAt) - time(a.createdAt);
    })
    .map(row);
  const ended = recent
    .filter((run) => runFinal(run.status) && !liveIds.has(run.id))
    .filter((run) => now - time(run.finishedAt || run.createdAt) <= RECENT_ENDED_MS)
    .sort((a, b) => time(b.finishedAt || b.createdAt) - time(a.finishedAt || a.createdAt))
    .slice(0, MAX_ENDED_ROWS)
    .map(row);
  return { live, ended };
}

/** How long a run has gone: since it started (or was created, while it
 * waits), until it finished or now. */
export function elapsedMs(run: Pick<Run, "createdAt" | "startedAt" | "finishedAt" | "status">, now: number): number {
  const from = time(run.startedAt) || time(run.createdAt);
  if (!from) return 0;
  const to = runFinal(run.status) ? time(run.finishedAt) || now : now;
  return Math.max(0, to - from);
}

/** "42s", "4m 05s", "1h 03m". */
export function formatElapsed(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${String(seconds % 60).padStart(2, "0")}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${String(minutes % 60).padStart(2, "0")}m`;
}

/** Who may pause, resume or cancel an autopilot: whoever started it, or an
 * administrator (a server without accounts has no viewer: its one user is). */
export function canSteer(viewer: Actor | null | undefined, createdBy: string): boolean {
  return !viewer || viewer.role === "admin" || viewer.id === createdBy;
}

/** The live autopilots, oldest first. */
export function liveAutopilots(views: Record<string, AutopilotView>): AutopilotView[] {
  return Object.values(views)
    .filter((view) => view.run.status === "running" || view.run.status === "paused")
    .sort((a, b) => time(a.run.createdAt) - time(b.run.createdAt));
}

/** What "Pause everything" is about to do, as the viewer can see it. Every
 * run listed is one the viewer may cancel (the hub lists no other), so only
 * autopilots can be someone else's. */
export interface PauseAllPlan {
  /** Running autopilots the viewer may pause. */
  pause: AutopilotView[];
  /** Running autopilots started by someone else: left alone. */
  othersAutopilots: number;
  /** Queued and offered runs: withdrawn. */
  pending: Run[];
  /** Running runs: cancelled only when asked. */
  running: Run[];
}

export function pauseAllPlan(
  viewer: Actor | null | undefined,
  active: Run[],
  views: Record<string, AutopilotView>,
): PauseAllPlan {
  const runningPilots = Object.values(views).filter((view) => view.run.status === "running");
  const pause = runningPilots.filter((view) => canSteer(viewer, view.run.createdBy));
  return {
    pause,
    othersAutopilots: runningPilots.length - pause.length,
    pending: active.filter((run) => run.status === "queued" || run.status === "offered"),
    running: active.filter((run) => run.status === "running"),
  };
}
