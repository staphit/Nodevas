import { Suspense, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { api, runFinal, runInteractive, type Run, type RunWorkspaceView } from "../api";
import { agentWho } from "../game/labels";
import { IconClose, IconFork, IconTrash } from "../icons";
import { useI18n } from "../i18n";
import { lazyWithRetry } from "../lazyWithRetry";
import { useEdition } from "../runners/edition";
import { runnerDisplayName } from "../runners/runnerName";
import { useApp } from "../store";
import { confirmDiscard, confirmMergeLocal } from "./confirmWorkspace";
import { RunDiffBody } from "./DiffViewer";
import { MergeControl, shortSha } from "./MergeApproval";
import { RunStatusBadge, WorkspaceChip } from "./NodeRuns";
import { OfferAskCard } from "./OfferAskCard";
import { declineText } from "./offerReasons";
import { openNodeRuns } from "./openRuns";
import { CIBadge, PRChip } from "./RunChanges";
import { RoleBadge } from "./RunEvidence";
import { safeExternalUrl } from "./runFormat";
import { RunTimeline } from "./RunTimeline";
import { selectRunById, useRuns } from "./runStore";
import { WorkspaceFiles } from "./WorkspaceFiles";
import { useWorkspace, workspaceCodeText, type WorkspaceTab } from "./workspaceStore";

// xterm stays out of the main bundle: only an interactive run's terminal
// ever loads it.
const RunTerminal = lazyWithRetry(() => import("./RunTerminal"));

const TABS: readonly WorkspaceTab[] = ["files", "changes", "terminal"];

type Translate = ReturnType<typeof useI18n>["t"];

/** The runner's word on the worktree, in one line: where it is, what is
 * uncommitted in it, and where a local merge would happen. */
function infoLine(t: Translate, run: Run, view: RunWorkspaceView | undefined): string[] {
  const info = view?.info;
  if (!info) return [];
  const runner = runnerDisplayName(t, run.runnerName) || run.runnerId;
  const parts = [info.exists ? t("runWorkspace.worktree.present", { runner }) : t("runWorkspace.worktree.gone")];
  if (info.exists && info.uncommitted > 0) parts.push(t("runWorkspace.uncommitted", { count: info.uncommitted }));
  if (info.baseCheckout === "repo") {
    parts.push(
      t("runWorkspace.info.baseRepo", {
        base: run.base,
        state: t(info.baseClean ? "runWorkspace.info.baseClean" : "runWorkspace.info.baseDirty"),
      }),
    );
  } else if (info.baseCheckout === "none") {
    parts.push(t("runWorkspace.info.baseNone", { base: run.base }));
  }
  return parts;
}

/** The latest workspace action's outcome: waiting, done (with a note) or
 * why it failed, the runner's own words under the translated code. */
function ActionStatus({ run }: { run: Run }) {
  const { t } = useI18n();
  const action = run.workspaceAction;
  if (!action) return null;
  const discard = action.action === "discard";
  const name = t(discard ? "runWorkspace.action.discard" : "runWorkspace.action.merge_local");
  if (action.status === "pending") {
    return (
      <p className="run-ws-status-line is-pending" role="status" aria-live="polite">
        <span className="run-merge-spinner" aria-hidden="true" />
        {t(discard ? "runWorkspace.discard.pending" : "runWorkspace.merge.pending")}
      </p>
    );
  }
  if (action.status === "done") {
    const commit = shortSha(action.mergeCommit || run.workspaceCommit);
    return (
      <p className="run-ws-status-line is-done" role="status">
        {discard
          ? t("runWorkspace.discard.done")
          : t("runWorkspace.merge.done", { base: action.base || run.base, commit })}
        {action.note && <small>{t("runWorkspace.note", { note: action.note })}</small>}
      </p>
    );
  }
  const code = action.status === "expired" ? "expired" : (action.code ?? "");
  const coded = workspaceCodeText(code, { base: action.base || run.base });
  return (
    <div className="run-ws-status-line is-failed" role="alert">
      {t("runWorkspace.failed", { action: name, reason: coded || action.reason || code })}
      {coded && action.reason && <small className="mono">{action.reason}</small>}
    </div>
  );
}

/** Merge locally and Discard, for the runner's owner only; each asks
 * first. A run with an open PR merges through it instead. */
function WorkspaceActions({ run, view }: { run: Run; view: RunWorkspaceView }) {
  const { t } = useI18n();
  const acting = useWorkspace((s) => s.acting[run.id]);
  const refusal = useWorkspace((s) => s.actionErrors[run.id]);
  const mergeLocal = useWorkspace((s) => s.mergeLocal);
  const discard = useWorkspace((s) => s.discard);
  const values = { base: run.base };
  const mergeBlocked = view.merge.blocked ? workspaceCodeText(view.merge.blocked, values) || view.merge.blocked : "";
  const discardBlocked = view.discard.blocked
    ? workspaceCodeText(view.discard.blocked, values) || view.discard.blocked
    : "";
  // A disabled Merge says why; a merge that cannot happen at all (the run
  // was merged or thrown away) is not offered.
  const showLocal =
    view.merge.mode === "local" ||
    (view.merge.mode === "" && Boolean(view.merge.blocked) && !FINAL_GATES.has(view.merge.blocked));
  const showDiscard =
    view.discard.allowed || (Boolean(view.discard.blocked) && !FINAL_GATES.has(view.discard.blocked));

  const onMerge = async () => {
    if (await confirmMergeLocal(t, run, view.info)) await mergeLocal(run);
  };
  const onDiscard = async () => {
    if (await confirmDiscard(t, run, view.info)) await discard(run);
  };

  return (
    <div className="run-ws-actions">
      {showLocal && (
        <button
          type="button"
          className="run-ws-merge"
          disabled={Boolean(view.merge.blocked) || Boolean(acting)}
          title={mergeBlocked || undefined}
          onClick={() => void onMerge()}
        >
          <IconFork size={13} />
          <span>{t("runWorkspace.merge.local", values)}</span>
        </button>
      )}
      {showDiscard && (
        <button
          type="button"
          className="run-ws-discard"
          disabled={!view.discard.allowed || Boolean(acting)}
          title={discardBlocked || undefined}
          onClick={() => void onDiscard()}
        >
          <IconTrash size={13} />
          <span>{t("runWorkspace.discard")}</span>
        </button>
      )}
      {refusal && (
        <div className="notify-message error run-ws-refusal" role="alert">
          {refusal}
        </div>
      )}
    </div>
  );
}

/** Gates after which the action will never be offered again. */
const FINAL_GATES = new Set(["discarded", "merged_local", "pr_merged"]);

function TabPanel({
  id,
  labelledBy,
  hidden,
  children,
}: {
  id: string;
  labelledBy: string;
  hidden: boolean;
  children: ReactNode;
}) {
  return (
    <div role="tabpanel" id={id} aria-labelledby={labelledBy} hidden={hidden} className="run-ws-panel">
      {children}
    </div>
  );
}

/**
 * One run's agent workspace: who works on what, where (runner, repo, branch
 * and base), how the run and its PR stand, and its worktree's files, its
 * changes against the base and its terminal or log. The runner's owner can
 * merge the branch (through the PR, or locally when there is none) or
 * discard the work.
 */
export function RunWorkspaceDialog({
  runId,
  tab,
  onClose,
}: {
  runId: string;
  tab: WorkspaceTab;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const single = useEdition() === "single";
  const run = useRuns((s) => selectRunById(s, runId));
  const view = useWorkspace((s) => s.views[runId]);
  const viewError = useWorkspace((s) => s.viewErrors[runId]);
  const loadView = useWorkspace((s) => s.loadView);
  const setTab = useWorkspace((s) => s.setTab);
  const nodeTitle = useApp((s) => (run ? s.graph?.nodes?.find((node) => node.id === run.nodeId)?.title : ""));
  const [missing, setMissing] = useState("");
  const titleId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  // Panels stay mounted once shown: the terminal keeps its connection and
  // the file tree what was opened.
  const [visited, setVisited] = useState<ReadonlySet<WorkspaceTab>>(() => new Set([tab]));
  useEffect(() => {
    setVisited((set) => (set.has(tab) ? set : new Set([...set, tab])));
  }, [tab]);

  // A run no list holds (a toast, another project's room): fetched once.
  const known = Boolean(run);
  useEffect(() => {
    if (known) return;
    let live = true;
    api
      .getRun(runId)
      .then((fetched) => live && useRuns.getState().putRun(fetched))
      .catch((error: unknown) => live && setMissing(error instanceof Error ? error.message : String(error)));
    return () => {
      live = false;
    };
  }, [known, runId]);

  // The gates follow the run: a finished run, an answered action, a merged PR.
  const status = run?.status;
  const workspace = run?.workspace;
  const actionStatus = run?.workspaceAction?.status;
  const prState = run?.prState;
  useEffect(() => {
    void loadView(runId);
  }, [runId, status, workspace, actionStatus, prState, loadView]);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    panelRef.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // A confirmation over the panel takes its own Escape, and so does the
      // agent's terminal.
      if (document.querySelector('[role="alertdialog"]')) return;
      if (event.target instanceof Element && event.target.closest(".run-term")) return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      if (previous?.isConnected) previous.focus();
    };
  }, [onClose]);

  const interactive = run ? runInteractive(run) : false;
  const tabLabel = (name: WorkspaceTab) =>
    name === "files"
      ? t("runWorkspace.tab.files")
      : name === "changes"
        ? t("runWorkspace.tab.changes")
        : interactive
          ? t("runWorkspace.tab.terminal")
          : t("runWorkspace.tab.log");
  const tabId = (name: WorkspaceTab) => `run-ws-${runId}-tab-${name}`;
  const panelId = (name: WorkspaceTab) => `run-ws-${runId}-panel-${name}`;
  const choose = (name: WorkspaceTab) => {
    setTab(name);
    document.getElementById(tabId(name))?.focus();
  };

  let body: ReactNode;
  if (!run) {
    body = <p className="run-ws-note">{missing ? t("runWorkspace.runGone") : t("common.loading")}</p>;
  } else {
    const runner = runnerDisplayName(t, run.runnerName) || run.runnerId;
    const info = infoLine(t, run, view);
    const pr = Boolean(safeExternalUrl(run.prUrl));
    body = (
      <>
        <div className="run-ws-summary">
          <div className="run-ws-chips">
            <RunStatusBadge status={run.status} run={run} />
            {run.global && <span className="run-ws-chip is-global">{t("globalAgents.chip")}</span>}
            <RoleBadge role={run.role} />
            <WorkspaceChip workspace={run.workspace} />
            <PRChip run={run} />
            {pr && <CIBadge status={run.ciStatus} />}
          </div>
          <dl className="run-ws-facts">
            {run.global ? (
              // A global agent has no node: its task's first line names it.
              <div>
                <dt>{t("globalAgents.col.task")}</dt>
                <dd className="run-ws-task">{run.title || run.id}</dd>
              </div>
            ) : (
              <div>
                <dt>{t("runWorkspace.node")}</dt>
                <dd>
                  <button
                    type="button"
                    className="run-link-button"
                    onClick={() => {
                      onClose();
                      openNodeRuns(run.nodeId, run.id);
                    }}
                  >
                    {nodeTitle || run.nodeId}
                  </button>
                </dd>
              </div>
            )}
            <div>
              <dt>{t(single ? "local.title" : "runWorkspace.runner")}</dt>
              <dd>
                {view && (
                  <span
                    className={`run-ws-dot${view.runnerOnline ? " is-online" : ""}`}
                    role="img"
                    aria-label={t(view.runnerOnline ? "runWorkspace.runnerOnline" : "runWorkspace.runnerOffline")}
                  />
                )}
                {runner}
              </dd>
            </div>
            <div>
              <dt>{t("runs.repo")}</dt>
              <dd>{run.repoName || run.repoId}</dd>
            </div>
            {(run.branch || run.base) && (
              <div>
                <dt>{t("runWorkspace.branch")}</dt>
                <dd className="mono">
                  {run.branch || run.base}
                  {run.branch && run.base ? ` ← ${run.base}` : ""}
                </dd>
              </div>
            )}
          </dl>
          {info.length > 0 && <p className="run-ws-info">{info.join(" · ")}</p>}
          {/* The app's own runner asks before it starts this run. */}
          <OfferAskCard run={run} className="run-ws-ask" />
          {run.status === "declined" && declineText(t, run, single) && (
            <p className="run-ws-info is-note">{declineText(t, run, single)}</p>
          )}
          {/* A global agent runs without a board: in the background it has
              nobody to ask. */}
          {run.global && !interactive && !runFinal(run.status) && (
            <p className="run-ws-info is-note">{t("globalAgents.noAsk")}</p>
          )}
          {viewError && !view && (
            <p className="run-ws-info is-error">{t("runWorkspace.loadFailed", { message: viewError })}</p>
          )}
          <ActionStatus run={run} />
          {/* The PR's own approval, with its own rules on who may press it. */}
          {view?.merge.mode === "pr" && <MergeControl run={run} className="run-ws-pr-merge" />}
          {view?.owner && <WorkspaceActions run={run} view={view} />}
        </div>
        <div className="run-ws-tablist" role="tablist" aria-label={t("runWorkspace.tabs")}>
          {TABS.map((name, index) => (
            <button
              key={name}
              type="button"
              role="tab"
              id={tabId(name)}
              aria-selected={tab === name}
              aria-controls={panelId(name)}
              tabIndex={tab === name ? 0 : -1}
              className={tab === name ? "active" : undefined}
              onClick={() => setTab(name)}
              onKeyDown={(event) => {
                if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
                  event.preventDefault();
                  const step = event.key === "ArrowRight" ? 1 : TABS.length - 1;
                  choose(TABS[(index + step) % TABS.length]);
                } else if (event.key === "Home" || event.key === "End") {
                  event.preventDefault();
                  choose(TABS[event.key === "Home" ? 0 : TABS.length - 1]);
                }
              }}
            >
              {tabLabel(name)}
            </button>
          ))}
        </div>
        <div className="run-ws-panels">
          {visited.has("files") && (
            <TabPanel id={panelId("files")} labelledBy={tabId("files")} hidden={tab !== "files"}>
              <WorkspaceFiles run={run} view={view} />
            </TabPanel>
          )}
          {visited.has("changes") && (
            <TabPanel id={panelId("changes")} labelledBy={tabId("changes")} hidden={tab !== "changes"}>
              <RunDiffBody run={run} />
            </TabPanel>
          )}
          {visited.has("terminal") && (
            <TabPanel id={panelId("terminal")} labelledBy={tabId("terminal")} hidden={tab !== "terminal"}>
              {interactive ? (
                <Suspense fallback={<p className="run-ws-note">{t("terminal.loading")}</p>}>
                  <RunTerminal run={run} />
                </Suspense>
              ) : (
                <RunTimeline run={run} />
              )}
            </TabPanel>
          )}
        </div>
      </>
    );
  }

  // Portalled over whichever view opened it (a run row, the war room, a
  // story room); a confirmation still stacks above it.
  return createPortal(
    <div
      className="run-ws-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div ref={panelRef} className="run-ws-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <header className="run-ws-head">
          <h2 id={titleId}>{t("runWorkspace.title", { agent: run ? agentWho(run.agent, run.model) : "" })}</h2>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("common.close")}>
            <IconClose size={14} />
          </button>
        </header>
        {body}
      </div>
    </div>,
    document.body,
  );
}

export default RunWorkspaceDialog;
