// A finished run's result in plain words (single-user edition): "Take a
// look" opens what the agent made, and "Apply to my folder" puts it into
// the person's folder. Underneath they are the run preview (a web page on
// the built-in runner's own loopback listener, else the run folder) and the
// owner's local merge, with its gates and refusals unchanged; only the words
// and a plain mapping of the refusals are new. Plain data and one async
// helper, tested without React.

import { api, WorkspaceActionError, type Run } from "../api";
import { useWorkspace, workspaceRefusal } from "./workspaceStore";

/** What a run's result offers: nothing (not this computer's, not finished
 * well, or it goes through a PR), Look and Apply, an apply under way, or
 * already in the folder. */
export type ResultState = "none" | "ready" | "applying" | "applied";

/** How long a result waits in Station's "Waiting on you": a week, so
 * yesterday's result is still there. */
export const APPLY_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

type ResultRun = Pick<
  Run,
  "status" | "commit" | "workspace" | "prUrl" | "prState" | "runnerId" | "kind" | "workspaceAction" | "merge"
>;

/**
 * Where a run's result stands. Only a run on this computer's built-in runner
 * (`builtInRunnerId`, undefined on a team hub) has one: an implement or test
 * run that succeeded with a commit and has no PR, whose workspace is as the
 * run left it and that no discard or PR merge is under way on.
 */
export function resultState(run: ResultRun, builtInRunnerId: string | undefined): ResultState {
  if (!builtInRunnerId || run.runnerId !== builtInRunnerId) return "none";
  if (run.workspace === "merged_local") return "applied";
  if (run.workspace || run.status !== "succeeded" || !run.commit) return "none";
  if (run.kind && run.kind !== "test") return "none";
  // A closed PR leaves the branch to merge here; any other PR is the way.
  if (run.prUrl && run.prState !== "closed") return "none";
  if (run.merge?.status === "pending") return "none";
  const action = run.workspaceAction;
  if (action?.status === "pending") return action.action === "merge_local" ? "applying" : "none";
  return "ready";
}

function millis(value: string | undefined): number {
  return value ? Date.parse(value) : NaN;
}

/**
 * The results waiting to be looked at and applied, newest first: each run
 * once, as its first copy in `runs` has it (callers put the freshest source
 * first), finished within `windowMs`.
 */
export function applyRuns(
  runs: readonly Run[],
  now: number,
  builtInRunnerId: string | undefined,
  windowMs = APPLY_WINDOW_MS,
): Run[] {
  if (!builtInRunnerId) return [];
  const seen = new Set<string>();
  const out: Run[] = [];
  for (const run of runs) {
    if (!run?.id || seen.has(run.id)) continue;
    seen.add(run.id);
    const state = resultState(run, builtInRunnerId);
    if (state !== "ready" && state !== "applying") continue;
    const finished = millis(run.finishedAt);
    if (!Number.isFinite(finished) || now - finished > windowMs) continue;
    out.push(run);
  }
  return out.sort((a, b) => millis(b.finishedAt) - millis(a.finishedAt));
}

const PLAIN_REFUSALS = new Set(["dirty_base", "conflict", "base_elsewhere", "head_moved"]);
const BUSY = new Set(["running", "pending", "in_use", "files_busy"]);
const STARTING = new Set(["offline", "outdated", "expired"]);
const GONE = new Set(["discarded", "merged_local", "pr_merged", "no_commit", "no_branch"]);

/** The plain sentence (a result.refuse.* key) for a workspace code; anything
 * without one of its own is "other", which carries the details. */
export function applyRefusalKey(code: string): string {
  if (PLAIN_REFUSALS.has(code)) return `result.refuse.${code}`;
  if (BUSY.has(code)) return "result.refuse.busy";
  if (STARTING.has(code)) return "result.refuse.offline";
  if (GONE.has(code)) return "result.refuse.gone";
  return "result.refuse.other";
}

/** What "Take a look" did: a page opened in a new tab (`url` is also the
 * link to offer), the folder opened instead (not a web page, or preview is
 * off here), nothing left to look at, or why nothing opened. */
export type LookOutcome =
  | { kind: "page"; url: string }
  | { kind: "blocked"; url: string }
  | { kind: "folder" }
  | { kind: "off" }
  | { kind: "gone" }
  | { kind: "failed"; reason: string };

/** Gates after which the run's files are no longer there. */
const FILES_GONE = new Set(["merged_local", "discarded", "no_worktree"]);

/** Only a page on this computer's own preview listener is opened. */
function previewPage(url: string | undefined): string {
  if (!url) return "";
  try {
    const parsed = new URL(url);
    const local = parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1";
    return parsed.protocol === "http:" && local && parsed.pathname.startsWith("/p/") ? parsed.href : "";
  } catch {
    return "";
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Opens what the agent made: a web page in a new tab, with no way back to
 * the app (`noopener,noreferrer`); anything else, or with preview off, the
 * run's folder: the preview answer's, else the workspace's (loaded now, not
 * when the row appeared).
 */
export async function lookAt(run: Pick<Run, "id" | "base">): Promise<LookOutcome> {
  let folder = "";
  let instead: "folder" | "off" = "folder";
  try {
    const preview = await api.previewRun(run.id);
    const page = preview.web ? previewPage(preview.url) : "";
    if (page) {
      try {
        // With noopener the browser returns null whether or not it opened
        // the tab, so a blocked tab is told only when opening throws.
        window.open(page, "_blank", "noopener,noreferrer");
        return { kind: "page", url: page };
      } catch {
        return { kind: "blocked", url: page };
      }
    }
    folder = preview.folder ?? "";
  } catch (error) {
    if (!(error instanceof WorkspaceActionError)) return { kind: "failed", reason: message(error) };
    if (FILES_GONE.has(error.blocked)) return { kind: "gone" };
    if (error.blocked !== "preview_off") return { kind: "failed", reason: workspaceRefusal(error, run) };
    instead = "off";
  }
  if (!folder) {
    await useWorkspace.getState().loadView(run.id);
    folder = useWorkspace.getState().views[run.id]?.info?.path ?? "";
  }
  if (!folder) return { kind: "gone" };
  try {
    await api.openFolder(folder);
  } catch (error) {
    return { kind: "failed", reason: message(error) };
  }
  return { kind: instead };
}
