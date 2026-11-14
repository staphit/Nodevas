// The viewer's global agents, as a table (cards on a phone): what is at
// work first, then what ended recently. Each row opens the run's workspace,
// stops it while it can be stopped, and once it ended merges its branch
// into its base locally or discards it (on the viewer's own runner, the
// workspace panel's own confirmations). A run with a pull request merges
// through it, from its workspace. A run the app's own runner asks about
// (single-user edition) carries its "Start this run?" card under its row; in
// that edition there is one machine, so no Runner column.

import { useEffect, useMemo, useRef, useState } from "react";

import { runCancellable, runFinal, runInteractive, type Run } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { useViewer } from "../components/SignIn";
import { agentWho } from "../game/labels";
import { IconCheck, IconCopy } from "../icons";
import { useI18n } from "../i18n";
import { useEdition } from "../runners/edition";
import { runnerDisplayName } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { confirmDiscard, confirmMergeLocal } from "../runs/confirmWorkspace";
import { RunStatusBadge, WorkspaceChip } from "../runs/NodeRuns";
import { OfferAskCard, runAsks } from "../runs/OfferAskCard";
import { declineText } from "../runs/offerReasons";
import { openRunWorkspace } from "../runs/openWorkspace";
import { CIBadge, PRChip } from "../runs/RunChanges";
import { formatCompact, formatCost, safeExternalUrl, totalTokens } from "../runs/runFormat";
import { useRuns } from "../runs/runStore";
import { useWorkspace } from "../runs/workspaceStore";
import { elapsedMs, formatElapsed } from "./control";
import { useGlobalAgents } from "./globalAgentsStore";

type Translate = ReturnType<typeof useI18n>["t"];

/** How often the elapsed times move on. */
const CLOCK_MS = 15_000;
/** How long a row opened from elsewhere stays highlighted. */
export const FOCUS_MS = 4_000;

/** A clock for the elapsed column. */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), CLOCK_MS);
    return () => clearInterval(timer);
  }, []);
  return now;
}

async function copyText(value: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(value);
  } catch {
    const field = document.createElement("textarea");
    field.value = value;
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.appendChild(field);
    field.select();
    document.execCommand("copy");
    field.remove();
  }
}

/** Whether the run's PR is open or merged: then it merges, or merged,
 * through the PR, and its workspace is where that happens. */
function hasLivePR(run: Run): boolean {
  return Boolean(safeExternalUrl(run.prUrl)) && (run.prState === "open" || run.prState === "merged");
}

/** What the owner may still do with an ended run's branch from the row. */
function localActions(run: Run, own: boolean): { merge: boolean; discard: boolean } {
  const none = { merge: false, discard: false };
  if (!own || !runFinal(run.status) || run.workspace || hasLivePR(run)) return none;
  if (run.workspaceAction?.status === "pending") return none;
  return { merge: Boolean(run.commit), discard: true };
}

export function GlobalAgentList() {
  const { t } = useI18n();
  const runs = useGlobalAgents((s) => s.runs);
  const more = useGlobalAgents((s) => s.more);
  const loaded = useGlobalAgents((s) => s.loaded);
  const loadingMore = useGlobalAgents((s) => s.loadingMore);
  const error = useGlobalAgents((s) => s.error);
  const focus = useGlobalAgents((s) => s.focus);
  const cancellingAll = useGlobalAgents((s) => s.cancellingAll);
  const cancelAllError = useGlobalAgents((s) => s.cancelAllError);
  const loads = useGlobalAgents((s) => s.runners);
  const globalById = useRuns((s) => s.globalById);
  const runners = useRunners((s) => s.runners);
  const viewer = useViewer();
  const single = useEdition() === "single";
  const now = useNow();

  // The run store's copy is the fresher one: a cancel or a workspace action
  // lands there first.
  const shown = useMemo(() => runs.map((run) => globalById[run.id] ?? run), [runs, globalById]);
  const active = shown.filter((run) => !runFinal(run.status));
  const ended = shown.filter((run) => runFinal(run.status));

  // The viewer's own runners: the global tab's loads are only theirs, and
  // a server without accounts has one owner.
  const own = useMemo(() => {
    const ids = new Set(loads.map((load) => load.runnerId));
    for (const runner of runners) if (viewer && runner.ownerId === viewer.id) ids.add(runner.id);
    return (runnerId: string) => !viewer || ids.has(runnerId);
  }, [loads, runners, viewer]);

  // A row opened from elsewhere: brought into view, highlighted a moment.
  useEffect(() => {
    if (!focus) return;
    const row = Array.from(document.querySelectorAll<HTMLElement>("[data-global-run]")).find(
      (element) => element.dataset.globalRun === focus,
    );
    row?.scrollIntoView?.({ block: "nearest" });
    const timer = setTimeout(() => {
      if (useGlobalAgents.getState().focus === focus) useGlobalAgents.getState().setFocus(null);
    }, FOCUS_MS);
    return () => clearTimeout(timer);
  }, [focus, shown.length]);

  const stopAll = async () => {
    const ok = await confirmAction({
      title: t("globalAgents.action.stopAllTitle", { count: active.length }),
      description: t("globalAgents.list.stopAllBody"),
      confirmLabel: t("globalAgents.action.stopAll"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (ok) await useGlobalAgents.getState().cancelAll();
  };

  const table = (list: Run[], label: string) => (
    <table className="global-agent-table" aria-label={label}>
      <thead>
        <tr>
          <th scope="col">{t("globalAgents.col.status")}</th>
          <th scope="col">{t("globalAgents.col.agent")}</th>
          <th scope="col">{t("globalAgents.col.task")}</th>
          <th scope="col">{t("globalAgents.col.branch")}</th>
          {!single && <th scope="col">{t("globalAgents.col.runner")}</th>}
          <th scope="col">{t("globalAgents.col.elapsed")}</th>
          <th scope="col">{t("globalAgents.col.usage")}</th>
          <th scope="col">{t("globalAgents.col.pr")}</th>
          <th scope="col">
            <span className="visually-hidden">{t("globalAgents.col.actions")}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {list.map((run) => (
          <GlobalAgentRow
            key={run.id}
            run={run}
            now={now}
            focused={run.id === focus}
            own={own(run.runnerId)}
            single={single}
          />
        ))}
      </tbody>
    </table>
  );

  return (
    <section className="station-panel global-agent-list" aria-labelledby="global-agent-list-title">
      <div className="station-panel-head">
        <h2 id="global-agent-list-title">{t("globalAgents.list.active", { count: active.length })}</h2>
        {active.length >= 2 && (
          <button
            type="button"
            className="ap-btn is-danger station-head-end"
            disabled={cancellingAll}
            onClick={() => void stopAll()}
          >
            {t("globalAgents.action.stopAll")}
          </button>
        )}
      </div>
      {cancelAllError && (
        <p className="notify-message error" role="alert">
          {cancelAllError}
        </p>
      )}
      {error && (
        <p className="notify-message error" role="alert">
          {t("globalAgents.list.loadFailed", { message: error })}
        </p>
      )}
      {!loaded ? (
        <p className="station-empty">{t("common.loading")}</p>
      ) : shown.length === 0 ? (
        <p className="station-empty">{t("globalAgents.list.empty")}</p>
      ) : (
        <>
          {active.length > 0 && table(active, t("globalAgents.list.active", { count: active.length }))}
          {ended.length > 0 && (
            <>
              <h3 className="global-agent-section">{t("globalAgents.list.recent")}</h3>
              {table(ended, t("globalAgents.list.recent"))}
            </>
          )}
          {more && (
            <button
              type="button"
              className="ap-btn global-agent-more"
              disabled={loadingMore}
              onClick={() => void useGlobalAgents.getState().loadMore()}
            >
              {loadingMore ? t("common.loading") : t("globalAgents.list.loadMore")}
            </button>
          )}
        </>
      )}
    </section>
  );
}

function BranchCell({ run }: { run: Run }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const repo = run.repoName || run.repoId;
  const where = run.branch ? `${run.branch}${run.base ? ` ← ${run.base}` : ""}` : run.base;
  return (
    <span className="global-agent-branch">
      <span className="mono">{[repo, where].filter(Boolean).join(" · ")}</span>
      {run.branch && (
        <button
          type="button"
          className="global-agent-copy"
          aria-label={copied ? t("globalAgents.form.copied") : t("globalAgents.list.copyBranch")}
          title={copied ? t("globalAgents.form.copied") : t("globalAgents.list.copyBranch")}
          onClick={() => {
            void copyText(run.branch).then(() => {
              setCopied(true);
              if (timer.current) clearTimeout(timer.current);
              timer.current = setTimeout(() => setCopied(false), 2000);
            });
          }}
        >
          {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
        </button>
      )}
    </span>
  );
}

function usageText(t: Translate, run: Run): string {
  const tokens = totalTokens(run.usage);
  const cost = formatCost(run.usage?.costUsd ?? run.costUsd);
  return [tokens > 0 ? t("station.control.tokens", { tokens: formatCompact(tokens) }) : "", cost]
    .filter(Boolean)
    .join(" · ");
}

function GlobalAgentRow({
  run,
  now,
  focused,
  own,
  single,
}: {
  run: Run;
  now: number;
  focused: boolean;
  own: boolean;
  /** Single-user edition: no Runner column, this computer's words. */
  single: boolean;
}) {
  const { t } = useI18n();
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const stopping = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const acting = useWorkspace((s) => s.acting[run.id]);
  const actionError = useWorkspace((s) => s.actionErrors[run.id]);
  const who = agentWho(run.agent, run.model);
  const runner = runnerDisplayName(t, run.runnerName) || run.runnerId;
  const interactive = runInteractive(run);
  const final = runFinal(run.status);
  const local = localActions(run, own);
  const usage = usageText(t, run);
  const noPRChip = run.noPR && final && !run.workspace && !run.prUrl;
  const declined = run.status === "declined" ? declineText(t, run, single) : "";

  const onStop = async () => {
    const ok = await confirmAction({
      title: t("globalAgents.action.stopTitle", { agent: who }),
      description: t("globalAgents.action.stopBody", { runner, branch: run.branch || run.base }),
      confirmLabel: t("globalAgents.action.stop"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (ok) await useGlobalAgents.getState().cancel(run.id);
  };
  const onMerge = async () => {
    if (await confirmMergeLocal(t, run, null)) await useWorkspace.getState().mergeLocal(run);
  };
  const onDiscard = async () => {
    if (await confirmDiscard(t, run, null)) await useWorkspace.getState().discard(run);
  };

  return (
    <>
      <tr className={`global-agent-row${focused ? " is-focus" : ""}`} data-global-run={run.id}>
        <td data-label={t("globalAgents.col.status")}>
          <span className="global-agent-chips">
            <RunStatusBadge status={run.status} run={run} />
            <WorkspaceChip workspace={run.workspace} />
            {noPRChip && (
              <span className="global-agent-chip">
                {t(single ? "globalAgents.list.noPRLocal" : "globalAgents.list.noPR")}
              </span>
            )}
          </span>
          {declined && <small className="global-agent-declined">{declined}</small>}
        </td>
        <td data-label={t("globalAgents.col.agent")}>
          <span className="global-agent-who">{who}</span>
          {interactive ? (
            <span className="global-agent-chip is-mode">{t("terminal.modeInteractive")}</span>
          ) : (
            !final && (
              <span className="global-agent-chip is-no-ask" title={t("globalAgents.noAsk")}>
                {t("globalAgents.noAskShort")}
              </span>
            )
          )}
        </td>
        <td data-label={t("globalAgents.col.task")} className="global-agent-task">
          {run.title || run.id}
        </td>
        <td data-label={t("globalAgents.col.branch")}>
          <BranchCell run={run} />
        </td>
        {!single && <td data-label={t("globalAgents.col.runner")}>{runner}</td>}
        <td data-label={t("globalAgents.col.elapsed")} className="global-agent-num">
          {formatElapsed(elapsedMs(run, now))}
        </td>
        <td data-label={t("globalAgents.col.usage")} className="global-agent-num">
          {usage || "—"}
        </td>
        <td data-label={t("globalAgents.col.pr")}>
          <span className="global-agent-chips">
            <PRChip run={run} />
            {run.prUrl && <CIBadge status={run.ciStatus} />}
          </span>
        </td>
        <td className="global-agent-actions">
          <span className="global-agent-buttons">
            <button type="button" className="ap-btn" onClick={() => openRunWorkspace(run.id)}>
              {t("globalAgents.action.workspace")}
            </button>
            {stopping ? (
              <button type="button" className="ap-btn" disabled>
                {t("station.control.stopping")}
              </button>
            ) : (
              runCancellable(run.status) && (
                <button
                  type="button"
                  className="ap-btn is-danger"
                  disabled={cancelling}
                  onClick={() => void onStop()}
                >
                  {t("globalAgents.action.stop")}
                </button>
              )
            )}
            {local.merge && (
              <button type="button" className="ap-btn" disabled={Boolean(acting)} onClick={() => void onMerge()}>
                {t("globalAgents.action.merge")}
              </button>
            )}
            {local.discard && (
              <button
                type="button"
                className="ap-btn is-danger"
                disabled={Boolean(acting)}
                onClick={() => void onDiscard()}
              >
                {t("globalAgents.action.discard")}
              </button>
            )}
          </span>
          {(cancelError || actionError) && (
            <span className="global-agent-error" role="alert">
              {cancelError || actionError}
            </span>
          )}
        </td>
      </tr>
      {runAsks(run) && (
        <tr className="global-agent-ask-row">
          <td colSpan={single ? 8 : 9}>
            <OfferAskCard run={run} />
          </td>
        </tr>
      )}
    </>
  );
}
