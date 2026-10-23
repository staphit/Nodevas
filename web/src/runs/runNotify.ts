import { create } from "zustand";
import { api, type Run } from "../api";
import { showSystemNotification } from "../agents/systemNotify";
import { translate } from "../i18n";
import { useApp } from "../store";
import { openNodeRuns } from "./openRuns";

/**
 * run-notify: the hub says a run finished, its CI turned red, or its merge
 * ended — without saying which. The run is fetched and read for the newest of
 * those that has not been told yet; each is told once per (run, kind) — per
 * merge request and per PR head where those can recur.
 */
export type RunNotifyKind = "succeeded" | "failed" | "ci-failed" | "merged" | "merge-failed";

export interface RunToast {
  id: number;
  runId: string;
  nodeId: string;
  kind: RunNotifyKind;
  title: string;
  body: string;
}

/** Older news than this is not news: a page that connects late should not
 * toast yesterday's merge because an unrelated push came for its run. */
const FRESH_MS = 15 * 60 * 1000;
/** Toasts on screen at once; the oldest goes first. */
const MAX_TOASTS = 3;
const TOAST_MS = 12_000;

function fresh(iso: string | undefined, now: number): boolean {
  if (!iso) return true;
  const at = Date.parse(iso);
  return !Number.isFinite(at) || now - at < FRESH_MS;
}

/** What a run-notify for this run is about, and the dedupe key for it, or
 * null when the run shows nothing worth telling. Newest lifecycle step first:
 * a merge outcome, then a red CI, then the run's own end. */
export function runNotifyKind(
  run: Run,
  now = Date.now(),
): { kind: RunNotifyKind; key: string } | null {
  const merge = run.merge;
  const mergeEnded =
    merge?.status === "merged" || merge?.status === "failed" || merge?.status === "expired";
  if (merge && mergeEnded && fresh(merge.finishedAt, now)) {
    return {
      kind: merge.status === "merged" ? "merged" : "merge-failed",
      key: `${run.id}:merge:${merge.id}`,
    };
  }
  if (run.prUrl && run.prState === "open" && run.ciStatus === "failure") {
    return { kind: "ci-failed", key: `${run.id}:ci-failed:${run.prHeadSha ?? ""}` };
  }
  if ((run.status === "succeeded" || run.status === "failed") && fresh(run.finishedAt, now)) {
    return { kind: run.status, key: `${run.id}:${run.status}` };
  }
  return null;
}

function nodeTitle(nodeId: string): string {
  const node = useApp.getState().graph?.nodes?.find((item) => item.id === nodeId);
  return node?.title || nodeId;
}

/** The toast's two lines, in the reader's language. */
export function runNotifyText(run: Run, kind: RunNotifyKind): { title: string; body: string } {
  const t = (key: string, values?: Record<string, string | number>) =>
    translate(key, undefined, values);
  const name = nodeTitle(run.nodeId);
  const pr = run.prNumber ? t("runs.pr", { number: run.prNumber }) : "";
  switch (kind) {
    case "succeeded":
      return { title: t("runNotify.succeeded", { name }), body: pr };
    case "failed":
      return { title: t("runNotify.failed", { name }), body: run.error ?? "" };
    case "ci-failed":
      return { title: t("runNotify.ciFailed", { number: run.prNumber ?? "" }), body: name };
    case "merged":
      return { title: t("runNotify.merged", { number: run.prNumber ?? "" }), body: name };
    case "merge-failed": {
      const reason =
        run.merge?.status === "expired"
          ? t("merge.expired")
          : run.merge?.reason || t("merge.failedNoReason");
      return { title: t("runNotify.mergeFailed", { reason }), body: name };
    }
  }
}

export interface RunToastState {
  toasts: RunToast[];
  /** Fetches the run and toasts what changed; resolves once handled. */
  handleRunNotify: (runId: string) => Promise<void>;
  dismiss: (id: number) => void;
  /** Dismisses the toast and opens the node's Runs tab on its run. */
  open: (id: number) => void;
}

let nextId = 1;
// Keys told this session, so a repeated push (or a reconnect) toasts once.
const told = new Set<string>();
const timers = new Map<number, ReturnType<typeof setTimeout>>();

/** Tests only: forget what was told. */
export function resetRunNotify(): void {
  told.clear();
  for (const timer of timers.values()) clearTimeout(timer);
  timers.clear();
  useRunToasts.setState({ toasts: [] });
}

export const useRunToasts = create<RunToastState>((set, get) => ({
  toasts: [],

  handleRunNotify: async (runId) => {
    if (!runId) return;
    let run: Run;
    try {
      run = await api.getRun(runId);
    } catch {
      // A run this viewer may not read, or the hub went away: nothing to say.
      return;
    }
    const what = runNotifyKind(run);
    if (!what || told.has(what.key)) return;
    told.add(what.key);
    const { title, body } = runNotifyText(run, what.kind);
    const toast: RunToast = {
      id: nextId++,
      runId: run.id,
      nodeId: run.nodeId,
      kind: what.kind,
      title,
      body,
    };
    set((state) => {
      const toasts = [...state.toasts, toast];
      for (const dropped of toasts.slice(0, Math.max(0, toasts.length - MAX_TOASTS))) {
        const timer = timers.get(dropped.id);
        if (timer) clearTimeout(timer);
        timers.delete(dropped.id);
      }
      return { toasts: toasts.slice(-MAX_TOASTS) };
    });
    timers.set(
      toast.id,
      setTimeout(() => get().dismiss(toast.id), TOAST_MS),
    );
    // The toast is on the page; a hidden page (a phone in a pocket, another
    // tab) also gets a system notification when this browser allows one.
    if (typeof document !== "undefined" && document.hidden) {
      showSystemNotification(title, body, `nodevas-run-${what.key}`, () =>
        openNodeRuns(run.nodeId, run.id),
      );
    }
  },

  dismiss: (id) => {
    const timer = timers.get(id);
    if (timer) clearTimeout(timer);
    timers.delete(id);
    set((state) => ({ toasts: state.toasts.filter((toast) => toast.id !== id) }));
  },

  open: (id) => {
    const toast = get().toasts.find((item) => item.id === id);
    get().dismiss(id);
    if (toast) openNodeRuns(toast.nodeId, toast.runId);
  },
}));
