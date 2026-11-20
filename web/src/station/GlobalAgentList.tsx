// The viewer's global agents, as a table (cards on a phone): what is at
// work first, then what ended recently. Follow-up chains collapse to their
// latest turn; multi-repo groups render as GlobalAgentGroup rows; contests
// render as contest rows with entry pips expanding to ContestView. Only one
// row is expanded at a time so at most one live dialog/terminal is mounted.

import { useEffect, useMemo, useRef, useState } from "react";

import { runCancellable, runFinal, runInteractive, type Run } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { useViewer } from "../components/SignIn";
import { agentWho } from "../game/labels";
import { CastPortrait, useFolderArt } from "../game/warroom/castArt";
import { IconCheck, IconCopy } from "../icons";
import { useI18n } from "../i18n";
import { useEdition, useLocalRunner } from "../runners/edition";
import { runnerDisplayName } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { confirmApply, confirmDiscard, confirmMergeLocal } from "../runs/confirmWorkspace";
import { ContestView } from "../runs/coord/ContestView";
import { useCoordStore } from "../runs/coord/coordStore";
import { OverlapNotice } from "../runs/coord/OverlapNotice";
import type { RunComposerHandle } from "../runs/dialog/RunComposer";
import { RunDialog } from "../runs/dialog/RunDialog";
import { RunStatusBadge, WorkspaceChip } from "../runs/NodeRuns";
import { OfferAskCard, runAsks } from "../runs/OfferAskCard";
import { declineText } from "../runs/offerReasons";
import { openRunWorkspace } from "../runs/openWorkspace";
import { ResultActions } from "../runs/ResultActions";
import { resultState } from "../runs/resultState";
import { CIBadge, PRChip } from "../runs/RunChanges";
import { formatCompact, formatCost, safeExternalUrl, totalTokens } from "../runs/runFormat";
import { selectRunById, useRuns } from "../runs/runStore";
import { runSignedOut, signedOutText } from "../runs/signedOut";
import { useWorkspace } from "../runs/workspaceStore";
import { useSimpleMode } from "../simple/simpleMode";
import { elapsedMs, formatElapsed } from "./control";
import { GlobalAgentGroup } from "./GlobalAgentGroup";
import { collapseGlobalChains, useGlobalAgents } from "./globalAgentsStore";

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
function localActions(
  run: Run,
  own: boolean,
  contest?: { state: string; winnerRunId?: string },
): { merge: boolean; discard: boolean } {
  const none = { merge: false, discard: false };
  if (!own || !runFinal(run.status) || run.workspace || hasLivePR(run)) return none;
  if (run.continuedBy) return none;
  if (run.contestId && (!contest || contest.state !== "decided" || contest.winnerRunId !== run.id)) return none;
  if (run.workspaceAction?.status === "pending") return none;
  return { merge: Boolean(run.commit), discard: true };
}

type ListEntry =
  | { kind: "run"; key: string; run: Run; turns: number; active: boolean }
  | { kind: "group"; key: string; groupId: string; runs: Run[]; active: boolean }
  | { kind: "contest"; key: string; contestId: string; runs: Run[]; active: boolean };

function buildEntries(latest: readonly Run[], turnCountById: Record<string, number>): ListEntry[] {
  const entries: ListEntry[] = [];
  const groups = new Map<string, { index: number; runs: Run[] }>();
  const contests = new Map<string, { index: number; runs: Run[] }>();

  for (const run of latest) {
    if (run.groupId) {
      const existing = groups.get(run.groupId);
      if (existing) {
        existing.runs.push(run);
        const item = entries[existing.index];
        if (item && item.kind === "group" && !runFinal(run.status)) item.active = true;
      } else {
        const list = [run];
        groups.set(run.groupId, { index: entries.length, runs: list });
        entries.push({
          kind: "group",
          key: `group:${run.groupId}`,
          groupId: run.groupId,
          runs: list,
          active: !runFinal(run.status),
        });
      }
      continue;
    }
    if (run.contestId) {
      const existing = contests.get(run.contestId);
      if (existing) {
        existing.runs.push(run);
        const item = entries[existing.index];
        if (item && item.kind === "contest" && !runFinal(run.status)) item.active = true;
      } else {
        const list = [run];
        contests.set(run.contestId, { index: entries.length, runs: list });
        entries.push({
          kind: "contest",
          key: `contest:${run.contestId}`,
          contestId: run.contestId,
          runs: list,
          active: !runFinal(run.status),
        });
      }
      continue;
    }
    entries.push({
      kind: "run",
      key: `run:${run.id}`,
      run,
      turns: turnCountById[run.id] ?? 1,
      active: !runFinal(run.status),
    });
  }
  return entries;
}

function pipTone(run: Run): "done" | "failed" | "cancelled" | "running" | "queued" {
  if (run.status === "succeeded") return "done";
  if (run.status === "failed" || run.status === "declined") return "failed";
  if (run.status === "cancelled") return "cancelled";
  if (run.status === "running") return "running";
  return "queued";
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
  const contests = useCoordStore((s) => s.contests);
  const viewer = useViewer();
  const single = useEdition() === "single";
  // Simple: no branch or pull request words, in the columns or the row.
  const simple = useSimpleMode();
  const localRunnerId = useLocalRunner()?.id;
  const now = useNow();
  // One row expanded at a time so at most one dialog/terminal is live.
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  // The run store's copy is the fresher one: a cancel or a workspace action
  // lands there first.
  const shown = useMemo(() => runs.map((run) => globalById[run.id] ?? run), [runs, globalById]);
  const { latest, turnCountById } = useMemo(() => collapseGlobalChains(shown), [shown]);
  const entries = useMemo(() => buildEntries(latest, turnCountById), [latest, turnCountById]);

  const activeRuns = useMemo(() => latest.filter((run) => !runFinal(run.status)), [latest]);
  const activeEntries = useMemo(() => entries.filter((entry) => entry.active), [entries]);
  const endedEntries = useMemo(() => entries.filter((entry) => !entry.active), [entries]);
  const hasActiveBackground = useMemo(() => activeRuns.some((run) => !runInteractive(run)), [activeRuns]);

  const colCount = (single ? 8 : 9) - (simple ? 2 : 0);

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

  const toggleExpand = (key: string, run?: Run) => {
    if (run && !selectRunById(useRuns.getState(), run.id)) {
      useRuns.getState().putRun(run);
    }
    setExpandedKey((prev) => (prev === key ? null : key));
  };

  const stopAll = async () => {
    const ok = await confirmAction({
      title: t("globalAgents.action.stopAllTitle", { count: activeRuns.length }),
      description: t("globalAgents.list.stopAllBody"),
      confirmLabel: t("globalAgents.action.stopAll"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (ok) await useGlobalAgents.getState().cancelAll();
  };

  const table = (list: ListEntry[], label: string) => (
    <table className="global-agent-table" aria-label={label}>
      <thead>
        <tr>
          <th scope="col">{t("globalAgents.col.status")}</th>
          <th scope="col">{t("globalAgents.col.agent")}</th>
          <th scope="col">{t("globalAgents.col.task")}</th>
          {!simple && <th scope="col">{t("globalAgents.col.branch")}</th>}
          {!single && <th scope="col">{t("globalAgents.col.runner")}</th>}
          <th scope="col">{t("globalAgents.col.elapsed")}</th>
          <th scope="col">{t("globalAgents.col.usage")}</th>
          {!simple && <th scope="col">{t("globalAgents.col.pr")}</th>}
          <th scope="col">
            <span className="visually-hidden">{t("globalAgents.col.actions")}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {list.map((entry) => {
          if (entry.kind === "group") {
            const focused = Boolean(focus && entry.runs.some((r) => r.id === focus));
            return (
              <tr
                key={entry.key}
                className={`global-agent-row is-group${focused ? " is-focus" : ""}`}
                data-testid={`global-group-${entry.groupId}`}
                data-global-group={entry.groupId}
              >
                <td colSpan={colCount} className="global-agent-group-cell">
                  <GlobalAgentGroup
                    groupId={entry.groupId}
                    runs={entry.runs}
                    expanded={expandedKey === entry.key}
                    onToggle={() => toggleExpand(entry.key)}
                    own={own(entry.runs[0]?.runnerId ?? "")}
                    single={single}
                    simple={simple}
                    focused={focused}
                  />
                </td>
              </tr>
            );
          }
          if (entry.kind === "contest") {
            return (
              <GlobalContestRow
                key={entry.key}
                contestId={entry.contestId}
                runs={entry.runs}
                now={now}
                expanded={expandedKey === entry.key}
                onToggle={() => toggleExpand(entry.key)}
                focused={Boolean(focus && entry.runs.some((r) => r.id === focus))}
                own={own(entry.runs[0]?.runnerId ?? "")}
                single={single}
                simple={simple}
                colCount={colCount}
              />
            );
          }
          const contest = entry.run.contestId ? contests[entry.run.contestId]?.contest : undefined;
          return (
            <GlobalAgentRow
              key={entry.key}
              run={entry.run}
              turns={entry.turns}
              now={now}
              expanded={expandedKey === entry.key}
              onToggle={() => toggleExpand(entry.key, entry.run)}
              focused={entry.run.id === focus}
              own={own(entry.run.runnerId)}
              single={single}
              simple={simple}
              plain={resultState(entry.run, localRunnerId, { contest }) !== "none"}
              contest={contest}
              colCount={colCount}
            />
          );
        })}
      </tbody>
    </table>
  );

  return (
    <section className="station-panel global-agent-list" aria-labelledby="global-agent-list-title">
      <div className="station-panel-head">
        <h2 id="global-agent-list-title">{t("globalAgents.list.active", { count: activeRuns.length })}</h2>
        {activeRuns.length >= 2 && (
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
      ) : latest.length === 0 ? (
        <p className="station-empty">{t("globalAgents.list.empty")}</p>
      ) : (
        <>
          {hasActiveBackground && <p className="global-agent-reply-note">{t("globalAgents.replyNote")}</p>}
          {activeEntries.length > 0 && table(activeEntries, t("globalAgents.list.active", { count: activeRuns.length }))}
          {endedEntries.length > 0 && (
            <>
              <h3 className="global-agent-section">{t("globalAgents.list.recent")}</h3>
              {table(endedEntries, t("globalAgents.list.recent"))}
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
  const folderArt = useFolderArt(run.repoId);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const repo = run.repoName || run.repoId;
  const where = run.branch ? `${run.branch}${run.base ? ` ← ${run.base}` : ""}` : run.base;
  return (
    <span className="global-agent-branch">
      {folderArt && (
        <img
          className="global-agent-folder-thumb"
          src={folderArt.url}
          alt=""
          loading="lazy"
          draggable={false}
          onError={folderArt.onError}
        />
      )}
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

/** What a run used: its tokens and cost; in Simple only what it cost. */
function usageText(t: Translate, run: Run, simple = false): string {
  const tokens = simple ? 0 : totalTokens(run.usage);
  const cost = formatCost(run.usage?.costUsd ?? run.costUsd);
  return [tokens > 0 ? t("station.control.tokens", { tokens: formatCompact(tokens) }) : "", cost]
    .filter(Boolean)
    .join(" · ");
}

function GlobalContestRow({
  contestId,
  runs,
  now,
  expanded,
  onToggle,
  focused,
  own,
  single,
  simple,
  colCount,
}: {
  contestId: string;
  runs: Run[];
  now: number;
  expanded: boolean;
  onToggle: () => void;
  focused: boolean;
  own: boolean;
  single: boolean;
  simple: boolean;
  colCount: number;
}) {
  const { t } = useI18n();
  const detail = useCoordStore((s) => s.contests[contestId]);
  const [cancelling, setCancelling] = useState(false);

  const entries = useMemo(
    () =>
      runs
        .filter((run) => run.contestLabel !== "J")
        .sort((a, b) => (a.contestLabel || "").localeCompare(b.contestLabel || "")),
    [runs],
  );
  const first = entries[0] ?? runs[0];
  const title = detail?.contest.title || first?.title || contestId;
  const entryCount = detail?.contest.entryCount || entries.length;
  const active = runs.some((run) => !runFinal(run.status));
  const state = detail?.contest.state || (active ? "running" : "ready");
  const stateKey =
    state === "running" || state === "judging" || state === "ready" || state === "decided" || state === "cancelled"
      ? `contest.state.${state}`
      : "contest.state.running";
  const runner = first ? runnerDisplayName(t, first.runnerName) || first.runnerId : "";
  const repo = first ? first.repoName || first.repoId : "";

  const onCancel = async () => {
    if (cancelling) return;
    setCancelling(true);
    try {
      await useCoordStore.getState().cancelContest(contestId);
    } finally {
      setCancelling(false);
    }
  };

  return (
    <>
      <tr
        className={`global-agent-row is-contest${focused ? " is-focus" : ""}`}
        data-testid={`global-contest-${contestId}`}
        data-global-contest={contestId}
      >
        <td data-label={t("globalAgents.col.status")}>
          <span className="global-agent-chips">
            <span className="global-agent-chip is-contest-state">{t(stateKey)}</span>
            <ul className="global-contest-pips" aria-label={t("contest.title", { count: entryCount })}>
              {entries.map((entry) => {
                const label = entry.contestLabel || "?";
                return (
                  <li
                    key={entry.id}
                    className={`global-contest-pip is-${pipTone(entry)}`}
                    aria-label={`${t("contest.entry", { label })}: ${entry.status}`}
                    title={`${t("common.withHint", { label: t("contest.entry", { label }), hint: agentWho(entry.agent, entry.model) })}: ${entry.status}`}
                  >
                    {label}
                  </li>
                );
              })}
            </ul>
          </span>
        </td>
        <td data-label={t("globalAgents.col.agent")}>
          <span className="global-agent-who">{t("contest.title", { count: entryCount })}</span>
        </td>
        <td data-label={t("globalAgents.col.task")} className="global-agent-task">
          <button
            type="button"
            className="global-agent-task-toggle"
            aria-expanded={expanded}
            onClick={onToggle}
          >
            {title}
          </button>
        </td>
        {!simple && (
          <td data-label={t("globalAgents.col.branch")}>
            <span className="mono">{repo}</span>
          </td>
        )}
        {!single && <td data-label={t("globalAgents.col.runner")}>{runner}</td>}
        <td data-label={t("globalAgents.col.elapsed")} className="global-agent-num">
          {first ? formatElapsed(elapsedMs(first, now)) : "—"}
        </td>
        <td data-label={t("globalAgents.col.usage")} className="global-agent-num">
          —
        </td>
        {!simple && <td data-label={t("globalAgents.col.pr")} />}
        <td className="global-agent-actions">
          <span className="global-agent-buttons">
            {active && own && (
              <button
                type="button"
                className="ap-btn is-danger"
                disabled={cancelling}
                onClick={() => void onCancel()}
              >
                {t("globalAgents.action.stop")}
              </button>
            )}
          </span>
        </td>
      </tr>
      {expanded && (
        <tr className="global-agent-dialog-row">
          <td colSpan={colCount}>
            <ContestView contestId={contestId} simple={simple} />
          </td>
        </tr>
      )}
    </>
  );
}

function GlobalAgentRow({
  run,
  turns,
  now,
  expanded,
  onToggle,
  focused,
  own,
  single,
  simple,
  plain,
  contest,
  colCount,
}: {
  run: Run;
  turns: number;
  now: number;
  expanded: boolean;
  onToggle: () => void;
  focused: boolean;
  own: boolean;
  /** Single-user edition: no Runner column, this computer's words. */
  single: boolean;
  /** Simple mode: no Branch or Pull request column, plain stop words. */
  simple: boolean;
  /** A result on this computer's own runner (resultState): the plain
   * result buttons stand in for Merge locally and say its refusals. */
  plain: boolean;
  contest?: { state: string; winnerRunId?: string };
  colCount: number;
}) {
  const { t } = useI18n();
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const stopping = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const acting = useWorkspace((s) => s.acting[run.id]);
  const actionError = useWorkspace((s) => s.actionErrors[run.id]);
  // A refused apply is the result buttons' to say; a refused discard is the
  // row's own, as on any other row.
  const applyRefused = useWorkspace((s) => s.actionKinds[run.id] === "merge");
  const overlaps = useCoordStore((s) => s.overlapsByRun[run.id]);
  const composerRef = useRef<RunComposerHandle>(null);

  const who = agentWho(run.agent, run.model);
  const runner = runnerDisplayName(t, run.runnerName) || run.runnerId;
  const interactive = runInteractive(run);
  const final = runFinal(run.status);
  const local = localActions(run, own, contest);
  const merge = local.merge && !plain;
  const usage = usageText(t, run, simple);
  const noPRChip = !simple && run.noPR && final && !run.workspace && !run.prUrl;
  const declined = run.status === "declined" ? declineText(t, run, single) : "";
  const overlapCount = run.overlapCount ?? 0;
  const waitingBehind = !final && Boolean(run.afterRun);
  // Its agent was signed out: how to sign in on this computer, or on a team
  // hub what the runner's owner runs there and whom anyone else asks.
  const builtIn = useRunners((s) => s.runners.some((r) => r.id === run.runnerId && r.builtIn));
  const signedOut = runSignedOut(run)
    ? signedOutText(t, run, builtIn ? "local" : own ? "owner" : "other")
    : "";

  useEffect(() => {
    if (expanded && overlapCount > 0) {
      void useCoordStore.getState().loadOverlaps(run.id);
    }
  }, [expanded, overlapCount, run.id]);

  const onStop = async () => {
    const ok = await confirmAction({
      title: t("globalAgents.action.stopTitle", { agent: who }),
      description: simple
        ? t("globalAgents.simple.stopBody")
        : t("globalAgents.action.stopBody", { runner, branch: run.branch || run.base }),
      confirmLabel: t("globalAgents.action.stop"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (ok) await useGlobalAgents.getState().cancel(run.id);
  };
  const onMerge = async () => {
    // Simple: the same merge, asked in the result buttons' words.
    const ok = simple ? await confirmApply(t, run.repoName) : await confirmMergeLocal(t, run, null);
    if (ok) await useWorkspace.getState().mergeLocal(run);
  };
  const onDiscard = async () => {
    if (await confirmDiscard(t, run, null)) await useWorkspace.getState().discard(run);
  };

  const pose = !final ? (interactive ? "speak" : "write") : "idle";

  return (
    <>
      <tr className={`global-agent-row${focused ? " is-focus" : ""}${expanded ? " is-expanded" : ""}`} data-global-run={run.id}>
        <td data-label={t("globalAgents.col.status")}>
          <span className="global-agent-chips">
            <CastPortrait
              agent={run.agent}
              model={run.model}
              scene={pose}
              fallback={null}
              className="global-agent-portrait"
            />
            <RunStatusBadge status={run.status} run={run} />
            <WorkspaceChip workspace={run.workspace} />
            {noPRChip && (
              <span className="global-agent-chip">
                {t(single ? "globalAgents.list.noPRLocal" : "globalAgents.list.noPR")}
              </span>
            )}
            {overlapCount > 0 && (
              <span className="global-agent-chip is-overlap">
                {t("overlap.badge", { count: overlapCount })}
              </span>
            )}
          </span>
          {waitingBehind && (
            <small className="global-agent-waiting-behind">
              {t(simple ? "runDialog.simple.ph.waitingBehind" : "busy.waitingBehind")}
            </small>
          )}
          {declined && <small className="global-agent-declined">{declined}</small>}
          {signedOut && <small className="global-agent-declined">{signedOut}</small>}
        </td>
        <td data-label={t("globalAgents.col.agent")}>
          <span className="global-agent-who">{who}</span>
          {interactive && <span className="global-agent-chip is-mode">{t("terminal.modeInteractive")}</span>}
        </td>
        <td data-label={t("globalAgents.col.task")} className="global-agent-task">
          <button
            type="button"
            className="global-agent-task-toggle"
            aria-expanded={expanded}
            onClick={onToggle}
          >
            {run.title || run.id}
          </button>
          {turns > 1 && (
            <span className="global-agent-chip is-turn">{t("runDialog.turn", { n: turns })}</span>
          )}
        </td>
        {!simple && (
          <td data-label={t("globalAgents.col.branch")}>
            <BranchCell run={run} />
          </td>
        )}
        {!single && <td data-label={t("globalAgents.col.runner")}>{runner}</td>}
        <td data-label={t("globalAgents.col.elapsed")} className="global-agent-num">
          {formatElapsed(elapsedMs(run, now))}
        </td>
        <td data-label={t("globalAgents.col.usage")} className="global-agent-num">
          {usage || "—"}
        </td>
        {!simple && (
          <td data-label={t("globalAgents.col.pr")}>
            <span className="global-agent-chips">
              <PRChip run={run} />
              {run.prUrl && <CIBadge status={run.ciStatus} />}
            </span>
          </td>
        )}
        <td className="global-agent-actions">
          {plain && <ResultActions run={run} compact />}
          <span className="global-agent-buttons">
            <button type="button" className="ap-btn" onClick={() => openRunWorkspace(run.id)}>
              {t(simple ? "globalAgents.simple.open" : "globalAgents.action.workspace")}
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
            {merge && (
              <button type="button" className="ap-btn" disabled={Boolean(acting)} onClick={() => void onMerge()}>
                {t(simple ? "result.apply" : "globalAgents.action.merge")}
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
          {(cancelError || (actionError && !(plain && applyRefused))) && (
            <span className="global-agent-error" role="alert">
              {cancelError || actionError}
            </span>
          )}
        </td>
      </tr>
      {expanded && (
        <tr className="global-agent-dialog-row">
          <td colSpan={colCount}>
            {overlaps && overlaps.length > 0 && (
              <OverlapNotice
                overlaps={overlaps}
                run={run}
                simple={simple}
                onTell={(text) => composerRef.current?.prefill(text)}
                onOpenFollowUp={(text) => composerRef.current?.prefill(text)}
              />
            )}
            <RunDialog runId={run.id} variant="plain" simple={simple} composerRef={composerRef} />
          </td>
        </tr>
      )}
      {runAsks(run) && (
        <tr className="global-agent-ask-row">
          <td colSpan={colCount}>
            <OfferAskCard run={run} />
          </td>
        </tr>
      )}
    </>
  );
}
