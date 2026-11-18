import { create } from "zustand";
import { api, pipelineEnded, type PipelineState, type Run } from "../api";
import { showSystemNotification } from "../agents/systemNotify";
import { formatLocalizedTime, translate } from "../i18n";
import { useAgents } from "../agents/agentStore";
import { agentName, agentWho } from "../game/labels";
import { builtInRunner } from "../runners/edition";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { openNodeRuns } from "./openRuns";
import { openRunWorkspace } from "./openWorkspace";
import { applyRefusalKey } from "./resultState";
import { runSignedOut, signInReader, signedOutText } from "./signedOut";
import { useRuns } from "./runStore";
import { workspaceCodeText } from "./workspaceStore";

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
 *
 * Single-user edition: the app's own runner asking before it starts a run
 * (the hub sends run-notify for an offer's first ask) is told too, so the
 * person sees "Waiting for your OK" even with no run list on screen; and a
 * result on that runner is told in plain words, as something to look at and
 * apply to the person's folder.
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
  | "pipeline-cancelled"
  // Agent workspace: the runner answered the owner's local merge or discard.
  | "workspace-merged"
  | "workspace-discarded"
  | "workspace-failed"
  // The app's own runner waits for its owner's OK before it starts the run.
  | "ask";

const WORKSPACE_KINDS: ReadonlySet<RunNotifyKind> = new Set([
  "workspace-merged",
  "workspace-discarded",
  "workspace-failed",
]);

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
  /** A global agent's run: no node to open, its workspace instead. */
  global?: boolean;
  kind: RunNotifyKind;
  title: string;
  body: string;
  /** A result applied on this computer's built-in runner: the person's
   * folder, offered as "Open my folder" on the toast. */
  folder?: string;
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
  // Waiting for an answer in this app: nothing else has happened to it yet.
  if (run.status === "offered" && run.ask?.where === "app") {
    return { kind: "ask", key: `${run.id}:ask:${run.ask.expiresAt}` };
  }
  // The owner's workspace action is the newest step a run can take.
  const action = run.workspaceAction;
  const actionEnded = action?.status === "done" || action?.status === "failed" || action?.status === "expired";
  if (action && actionEnded && fresh(action.finishedAt, now)) {
    return {
      kind:
        action.status !== "done"
          ? "workspace-failed"
          : action.action === "discard"
            ? "workspace-discarded"
            : "workspace-merged",
      key: `${run.id}:workspace:${action.id}`,
    };
  }
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

/** What the run worked on: a global agent's task title, else its node's. */
function runSubject(run: Run): string {
  return run.global ? run.title || run.branch || run.id : nodeTitle(run.nodeId);
}

/** Whether the run is on this computer's built-in runner (the hub's word,
 * so single-user edition only). */
function onBuiltInRunner(run: Run): boolean {
  const local = builtInRunner(useRunners.getState().runners);
  return Boolean(local && run.runnerId === local.id);
}

/** The person's folder a result on this computer was applied to; "" for
 * anything else. */
function appliedFolder(run: Run, kind: RunNotifyKind): string {
  if (kind !== "workspace-merged") return "";
  const local = builtInRunner(useRunners.getState().runners);
  if (!local || run.runnerId !== local.id) return "";
  return local.info?.repos?.find((repo) => repo.id === run.repoId)?.path ?? "";
}

/** A failed run's second line: how to sign its agent in when that is why it
 * failed (who may sign in decides the words), else what the runner said. */
function failedBody(run: Run, t: (key: string, values?: Record<string, string | number>) => string): string {
  if (!runSignedOut(run)) return run.error ?? "";
  const reader = signInReader(run, useRunners.getState().runners, useAgents.getState().viewer);
  return signedOutText(t, run, reader);
}

/** The toast's two lines, in the reader's language. */
export function runNotifyText(
  run: Run,
  kind: RunNotifyKind,
  pipeline: PipelineState | null = null,
): { title: string; body: string } {
  const t = (key: string, values?: Record<string, string | number>) =>
    translate(key, undefined, values);
  const name = runSubject(run);
  const pr = run.prNumber ? t("runs.pr", { number: run.prNumber }) : "";
  // A result on this computer: looked at and applied, in plain words.
  if (onBuiltInRunner(run)) {
    const agent = agentWho(run.agent, run.model);
    if (kind === "succeeded" && run.commit && !run.prUrl) {
      return { title: t("result.toast.finished", { agent, title: name }), body: t("result.toast.finishedBody") };
    }
    if (kind === "workspace-merged") return { title: t("result.toast.applied", { title: name }), body: "" };
    // Its agent is here but signed out: what to do, not the CLI's English.
    if (kind === "failed" && runSignedOut(run)) {
      return { title: t("result.toast.signedOut", { agent: agentName(run.agent) }), body: failedBody(run, t) };
    }
    // An apply refused after the fact: the plain sentence the result buttons
    // say, which always says nothing was changed.
    if (kind === "workspace-failed" && run.workspaceAction?.action === "merge_local") {
      const action = run.workspaceAction;
      const code = action.status === "expired" ? "expired" : (action.code ?? "");
      const reason = [workspaceCodeText(code, { base: run.base }), action.reason].filter(Boolean).join(" · ") || code;
      return { title: t("result.toast.notApplied", { title: name }), body: t(applyRefusalKey(code), { reason }) };
    }
  }
  // A global agent has no node to name: the toast says whose task it was.
  if (run.global && (kind === "succeeded" || kind === "failed")) {
    const agent = agentWho(run.agent, run.model);
    return kind === "succeeded"
      ? { title: t("globalAgents.toast.finished", { agent, title: name }), body: pr }
      : { title: t("globalAgents.toast.failed", { agent, title: name }), body: failedBody(run, t) };
  }
  switch (kind) {
    case "ask":
      return {
        title: t("offerAsk.notify", { agent: agentName(run.agent), repo: run.repoName || run.repoId }),
        body: name,
      };
    case "succeeded":
      return { title: t("runNotify.succeeded", { name }), body: pr };
    case "failed":
      return { title: t("runNotify.failed", { name }), body: failedBody(run, t) };
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
    case "workspace-merged":
      return {
        title: t("runWorkspace.toast.merged", { branch: run.branch, base: run.workspaceAction?.base || run.base }),
        body: name,
      };
    case "workspace-discarded":
      return { title: t("runWorkspace.toast.discarded", { agent: agentWho(run.agent, run.model) }), body: name };
    case "workspace-failed": {
      const action = run.workspaceAction;
      const code = action?.status === "expired" ? "expired" : (action?.code ?? "");
      const reason = workspaceCodeText(code, { base: run.base }) || action?.reason || "";
      return {
        title: t("runWorkspace.toast.failed", {
          action: t(action?.action === "discard" ? "runWorkspace.action.discard" : "runWorkspace.action.merge_local"),
          agent: agentWho(run.agent, run.model),
        }),
        body: [reason, name].filter(Boolean).join(" · "),
      };
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
function openToast(toast: Pick<RunToast, "kind" | "nodeId" | "runId" | "global">): void {
  if (toast.kind === "budget") {
    window.dispatchEvent(new CustomEvent("nodevas-open-budget"));
    return;
  }
  if (toast.global || WORKSPACE_KINDS.has(toast.kind) || (toast.kind === "ask" && !toast.nodeId)) {
    openRunWorkspace(toast.runId);
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
  // Simple (single-user edition) says it as its HUD does, "today's limit",
  // with the reset in this computer's time rather than UTC.
  const text = simpleOn()
    ? { title: t("simple.notify.limitReached"), body: t("simple.notify.limitBody", { time: formatLocalizedTime(nextUtcDay()) }) }
    : { title: t("runNotify.budget"), body: t("budget.exceeded", { limit: t(`budget.limit.${exceeded}`) }) };
  push({ runId: "", nodeId: "", kind: "budget", ...text }, `nodevas-${key}`);
}

/** Whether the Simple preset is on, outside React: the person chose it and
 * this is the single-user edition, whose built-in runner only it has
 * (useSimpleMode is the hook's way). */
function simpleOn(): boolean {
  return useApp.getState().preferences.experience === "simple" && Boolean(builtInRunner(useRunners.getState().runners));
}

/** When the hub's day, and so the daily budget, starts again. */
function nextUtcDay(now = new Date()): Date {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + 1));
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
    // A workspace action's answer: the open panel and the rows follow it now,
    // before the list refresh. A global run is held either way: its toast
    // opens its workspace, which reads it from the store. So is a run that
    // waits for an answer, so its card shows wherever the run does.
    if (run.workspaceAction || run.global || run.ask) useRuns.getState().putRun(run);
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
    const folder = appliedFolder(run, what.kind);
    get().push(
      {
        runId: run.id,
        nodeId: run.nodeId,
        kind: what.kind,
        title,
        body,
        ...(run.global ? { global: true } : {}),
        ...(folder ? { folder } : {}),
      },
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
