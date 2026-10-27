import { create } from "zustand";
import { api, pipelineEnded, type PipelineState, type Run } from "../api";
import { showSystemNotification } from "../agents/systemNotify";
import { translate } from "../i18n";
import { useApp } from "../store";
import { openNodeRuns } from "./openRuns";
import { useRuns } from "./runStore";

/**
 * run-notify: the hub says a run finished, its CI turned red, or its merge
 * ended — without saying which. The run is fetched and read for the newest of
 * those that has not been told yet; each is told once per (run, kind) — per
 * merge request and per PR head where those can recur.
 *
 * A run that failed on its agent's usage limit (rate limit or used-up quota)
 * says so rather than plain "failed". And since a run's end is when the
 * viewer's daily budget can run out, each run-notify also checks it, telling
 * once per UTC day and limit.
 */
export type RunNotifyKind =
  | "succeeded"
  | "failed"
  // The agent hit its usage limit: rate limited, or quota/credit used up.
  | "limit"
  // The viewer's daily run budget is used up.
  | "budget"
  | "ci-failed"
  | "merged"
  | "merge-failed"
  // W5: a pipeline (implement → review) ended; told instead of its last run.
  | "pipeline-approved"
  | "pipeline-changes-left"
  | "pipeline-failed"
  | "pipeline-cancelled";

const PIPELINE_KINDS: Record<string, RunNotifyKind> = {
  approved: "pipeline-approved",
  changes_left: "pipeline-changes-left",
  failed: "pipeline-failed",
  cancelled: "pipeline-cancelled",
};

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
 * a merge outcome, then a red CI, then the end of the pipeline the run is a
 * step of (when `pipeline` says it ended), then the run's own end. */
export function runNotifyKind(
  run: Run,
  now = Date.now(),
  pipeline: PipelineState | null = null,
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
  if (
    pipeline &&
    run.pipelineId === pipeline.id &&
    pipelineEnded(pipeline.status) &&
    PIPELINE_KINDS[pipeline.status] &&
    fresh(run.finishedAt, now)
  ) {
    return {
      kind: PIPELINE_KINDS[pipeline.status],
      key: `pipeline:${pipeline.id}:${pipeline.status}`,
    };
  }
  if (
    run.status === "failed" &&
    (run.failureKind === "rate_limit" || run.failureKind === "quota") &&
    fresh(run.finishedAt, now)
  ) {
    return { kind: "limit", key: `${run.id}:limit` };
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
export function runNotifyText(
  run: Run,
  kind: RunNotifyKind,
  pipeline: PipelineState | null = null,
): { title: string; body: string } {
  const t = (key: string, values?: Record<string, string | number>) =>
    translate(key, undefined, values);
  const name = nodeTitle(run.nodeId);
  const pr = run.prNumber ? t("runs.pr", { number: run.prNumber }) : "";
  switch (kind) {
    case "succeeded":
      return { title: t("runNotify.succeeded", { name }), body: pr };
    case "failed":
      return { title: t("runNotify.failed", { name }), body: run.error ?? "" };
    case "limit":
      return {
        title: t(run.failureKind === "quota" ? "runNotify.quota" : "runNotify.rateLimit", {
          agent: run.agent,
          name,
        }),
        body: run.error ?? "",
      };
    case "budget":
      // Budget toasts are made by checkBudget, not from a run.
      return { title: "", body: "" };
    case "ci-failed":
      return { title: t("runNotify.ciFailed", { number: run.prNumber ?? "" }), body: name };
    case "merged":
      return { title: t("runNotify.merged", { number: run.prNumber ?? "" }), body: name };
    case "pipeline-approved":
    case "pipeline-changes-left":
    case "pipeline-failed":
    case "pipeline-cancelled": {
      const key = {
        "pipeline-approved": "runNotify.pipelineApproved",
        "pipeline-changes-left": "runNotify.pipelineChangesLeft",
        "pipeline-failed": "runNotify.pipelineFailed",
        "pipeline-cancelled": "runNotify.pipelineCancelled",
      }[kind];
      const rounds =
        pipeline && pipeline.round > 0
          ? t("pipeline.round", {
              round: pipeline.round,
              max: pipeline.pipeline?.maxRounds || pipeline.round,
            })
          : "";
      const detail = kind === "pipeline-failed" ? (run.error ?? "") : pr;
      return { title: t(key, { name }), body: [rounds, detail].filter(Boolean).join(" · ") };
    }
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
  /** Shows a toast, and a system notification (tagged tag) while the page is
   * hidden. */
  push: (toast: Omit<RunToast, "id">, tag: string) => void;
  /** Dismisses the toast and opens what it is about: the node's Runs tab on
   * its run, or the budget. */
  open: (id: number) => void;
}

/** Opens what a toast is about. */
function openToast(toast: Pick<RunToast, "kind" | "nodeId" | "runId">): void {
  if (toast.kind === "budget") {
    window.dispatchEvent(new CustomEvent("nodevas-open-budget"));
    return;
  }
  openNodeRuns(toast.nodeId, toast.runId);
}

/** UTC day, as the hub counts the budget. */
function utcDay(now = new Date()): string {
  return now.toISOString().slice(0, 10);
}

/**
 * Tells once per UTC day and limit that the viewer's daily budget is used up.
 * A viewer with no limits set has nothing to be told.
 */
async function checkBudget(push: RunToastState["push"]): Promise<void> {
  let status;
  try {
    status = await api.getRunBudget();
  } catch {
    return;
  }
  const exceeded = status?.exceeded;
  if (!exceeded) return;
  const key = `budget:${utcDay()}:${exceeded}`;
  if (told.has(key)) return;
  told.add(key);
  const t = (k: string, values?: Record<string, string | number>) => translate(k, undefined, values);
  push(
    {
      runId: "",
      nodeId: "",
      kind: "budget",
      title: t("runNotify.budget"),
      body: t("budget.exceeded", { limit: t(`budget.limit.${exceeded}`) }),
    },
    `nodevas-${key}`,
  );
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
    // A pipeline step: whether the pipeline ended decides what to tell.
    let pipeline: PipelineState | null = null;
    if (run.pipelineId) {
      try {
        pipeline = await api.getPipeline(run.pipelineId);
        if (pipeline?.id) useRuns.getState().putPipeline(pipeline);
        else pipeline = null;
      } catch {
        pipeline = null;
      }
    }
    void checkBudget(get().push);
    const what = runNotifyKind(run, Date.now(), pipeline);
    if (!what || told.has(what.key)) return;
    told.add(what.key);
    const { title, body } = runNotifyText(run, what.kind, pipeline);
    get().push(
      { runId: run.id, nodeId: run.nodeId, kind: what.kind, title, body },
      `nodevas-run-${what.key}`,
    );
  },

  push: (fields, tag) => {
    const toast: RunToast = { id: nextId++, ...fields };
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
      showSystemNotification(toast.title, toast.body, tag, () => openToast(toast));
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
    if (toast) openToast(toast);
  },
}));
