import { useCallback, useEffect, useMemo, useState } from "react";
import { runCancellable, runFinal, runWaitsForLimit, type PipelineState, type Run } from "../api";
import { confirmCancelRun } from "./confirmCancelRun";
import { IconFolder, IconFork, IconSend, IconTerminal } from "../icons";

import { formatLocalizedDateTime, formatLocalizedTime, useI18n } from "../i18n";
import { useEdition } from "../runners/edition";
import { runnerDisplayName } from "../runners/runnerName";
import { useRunners } from "../runners/runnerStore";
import { useViewer } from "../components/SignIn";
import { openSettings } from "../settings/openSettings";
import { useSimpleMode } from "../simple/simpleMode";
import { relativeTime } from "../runners/RunnersModal";
import {
  formatCompact,
  formatCost,
  paragraphs,
  runHasChanges,
  totalTokens,
} from "./runFormat";
import { ContestDecide } from "./coord/ContestDecide";
import { RunDiffDialog } from "./DiffViewer";
import { MergeControl } from "./MergeApproval";
import { NodeSessions } from "./NodeSessions";
import { RunChangesSummary } from "./RunChanges";
import { RunModeTabs } from "./RunModeTabs";
import { RunTimeline } from "./RunTimeline";
import { useRuns } from "./runStore";
import { openRunWorkspace } from "./openWorkspace";
import { useNodeType } from "../nodeTypeTheme";
import { GuardView, JudgeChecks, RoleBadge, VerifyEvidence } from "./RunEvidence";
import { OfferAskCard, runAsksInApp } from "./OfferAskCard";
import { declineFix, declineText } from "./offerReasons";
import { runSignedOut, signInReader, signedOutText } from "./signedOut";

export { RunAskItem, RunEventItem } from "./RunTimeline";

/** The hub's cap on GET /api/runs. */
const RUN_LIST_CAP = 50;

const KNOWN_STATUSES = new Set([
  "queued",
  "offered",
  "declined",
  "running",
  "succeeded",
  "failed",
  "cancelled",
]);

/** A run's status word. Given the run, an offered one says more: "Waiting
 * for your OK" while the app's own runner asks, and in the single-user
 * edition "Starting…" otherwise (nobody accepts anything there). */
export function RunStatusBadge({ status, run }: { status: string; run?: Pick<Run, "status" | "ask"> | null }) {
  const { t } = useI18n();
  const single = useEdition() === "single";
  const known = KNOWN_STATUSES.has(status);
  const asking = status === "offered" && runAsksInApp(run);
  const label = asking
    ? t("runs.status.asking")
    : status === "offered" && single && !run?.ask
      ? t("runs.status.starting")
      : known
        ? t(`runs.status.${status}`)
        : status;
  return (
    <span className={`run-status run-status-${known ? status : "unknown"}${asking ? " is-asking" : ""}`}>{label}</span>
  );
}

/** The detail's line under the status: what an offered run waits for, why
 * a declined one did not start, in the edition's own words. */
export function runStatusHint(
  t: (key: string, values?: Record<string, string | number>) => string,
  run: Run,
  single: boolean,
): string {
  if (!KNOWN_STATUSES.has(run.status)) return "";
  if (run.status === "offered") {
    // The ask card says it all.
    if (run.ask) return "";
    if (single) return t("runs.statusHint.offeredLocal");
  }
  if (run.status === "queued" && single && !runWaitsForLimit(run)) return t("runs.statusHint.queuedLocal");
  if (run.status === "declined") return declineText(t, run, single) || t("runs.statusHint.declined");
  return t(`runs.statusHint.${run.status}`);
}

function UsageText({ run }: { run: Run }) {
  const { t } = useI18n();
  const tokens = totalTokens(run.usage);
  const cost = formatCost(run.usage?.costUsd);
  if (!tokens && !cost) return null;
  return (
    <span className="run-usage">
      {tokens > 0 && t("runs.tokens", { count: formatCompact(tokens) })}
      {tokens > 0 && cost && " · "}
      {cost}
    </span>
  );
}

/** The run a fallback run retried, as the list knows it. */
export interface RetryInfo {
  /** Its number in the list; 0 when unknown or not listed. */
  number: number;
  failureKind?: string;
  /** The retry continues the earlier run's partial work (a handoff). */
  continues?: boolean;
}

/** "agent/model", or the agent alone. */
function agentLabel(run: { agent: string; model?: string }): string {
  return run.model ? `${run.agent}/${run.model}` : run.agent;
}

const KNOWN_FAILURES = new Set([
  "rate_limit",
  "quota",
  "model_unavailable",
  "agent_unavailable",
  "task",
]);

/** "Rate limited", "Quota used up"… for a run's failure kind. */
export function failureLabel(t: (key: string) => string, kind: string): string {
  return KNOWN_FAILURES.has(kind) ? t(`runs.failure.${kind}`) : kind;
}

const SHOWN_KINDS = new Set(["review", "revise", "test", "verify", "judge"]);

export function RunKindBadge({ kind }: { kind?: string }) {
  const { t } = useI18n();
  if (!kind || !SHOWN_KINDS.has(kind)) return null;
  return <span className={`run-kind run-kind-${kind}`}>{t(`runs.kind.${kind}`)}</span>;
}

const SHOWN_VERDICTS = new Set(["approve", "changes", "pass", "fail"]);

export function VerdictChip({ verdict }: { verdict?: string }) {
  const { t } = useI18n();
  if (!verdict || !SHOWN_VERDICTS.has(verdict)) return null;
  return (
    <span className={`run-verdict run-verdict-${verdict}`}>{t(`runs.verdict.${verdict}`)}</span>
  );
}

/** "Fell back from #3 (rate limited)". */
export function retryText(
  t: (key: string, values?: Record<string, string | number>) => string,
  retry: RetryInfo,
): string {
  if (retry.continues) {
    return retry.number > 0
      ? t("runs.continuesFrom", { number: retry.number })
      : t("runs.continuesFromUnknown");
  }
  const reason = retry.failureKind ? failureLabel(t, retry.failureKind) : "";
  if (retry.number > 0) {
    return reason
      ? t("runs.retryOfReason", { number: retry.number, reason })
      : t("runs.retryOf", { number: retry.number });
  }
  return reason ? t("runs.retryOfUnknownReason", { reason }) : t("runs.retryOfUnknown");
}

const WORKSPACE_STATES = new Set(["merged_local", "discarded"]);

/** "Merged locally" or "Discarded": what the owner did with the run's
 * worktree and branch. Nothing while they are as the run left them. */
export function WorkspaceChip({ workspace }: { workspace?: string }) {
  const { t } = useI18n();
  // Simple: applied to the person's folder, not "merged locally".
  const simple = useSimpleMode();
  if (!workspace || !WORKSPACE_STATES.has(workspace)) return null;
  const key = simple && workspace === "merged_local" ? "simple.workspace.merged_local" : `runWorkspace.state.${workspace}`;
  return <span className={`run-workspace-chip is-${workspace}`}>{t(key)}</span>;
}

/** The W5 line under a run: its job kind, a review's verdict, why it failed
 * and which run it falls back from. Nothing for a plain W1 run. */
function RunTags({
  run,
  retry,
  next = null,
}: {
  run: Run;
  retry: RetryInfo | null;
  /** The run that took this one's work on, if any. */
  next?: Run | null;
}) {
  const { t, language } = useI18n();
  const failure = run.status === "failed" && run.failureKind ? run.failureKind : "";
  const kind = SHOWN_KINDS.has(run.kind ?? "");
  const verdict = SHOWN_VERDICTS.has(run.verdict ?? "");
  const waiting = runWaitsForLimit(run);
  const handedTo = run.handoffBranch && next ? next : null;
  const workspace = WORKSPACE_STATES.has(run.workspace ?? "");
  if (!run.role && !kind && !verdict && !failure && !retry && !waiting && !handedTo && !workspace) return null;
  return (
    <span className="run-row-tags">
      <RoleBadge role={run.role} />
      <WorkspaceChip workspace={run.workspace} />
      <RunKindBadge kind={run.kind} />
      <VerdictChip verdict={run.verdict} />
      {failure && (
        <span className={`run-failure run-failure-${failure}`}>{failureLabel(t, failure)}</span>
      )}
      {retry && <span className="run-retry">{retryText(t, retry)}</span>}
      {handedTo && (
        <span className="run-handoff">{t("runs.handedOffTo", { target: agentLabel(handedTo) })}</span>
      )}
      {waiting && run.retryAt && (
        <span className="run-limit-wait" title={formatLocalizedDateTime(run.retryAt, language)}>
          {t("runs.limitWait", { time: formatLocalizedTime(run.retryAt, language) })}
        </span>
      )}
    </span>
  );
}

/** A run's handoff, in its detail: the wait for a usage limit (with retry
 * now and cancel), the partial work it kept and who took it on, and the run
 * whose work it continues. A note is the earlier run's own account, shown as
 * plain text, as the next agent got it. */
function RunHandoff({
  run,
  prev,
  next,
  prevNumber,
  nextNumber,
  onSelect,
  onCancel,
}: {
  run: Run;
  prev: Run | null;
  next: Run | null;
  prevNumber: number;
  nextNumber: number;
  onSelect?: (runId: string) => void;
  onCancel: () => void;
}) {
  const { t, language } = useI18n();
  const retrying = useRuns((s) => Boolean(s.retrying[run.id]));
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const retryRunNow = useRuns((s) => s.retryRunNow);
  const waiting = runWaitsForLimit(run) && run.retryAt;
  if (!waiting && !run.handoffBranch && !run.continuesFrom) return null;
  const link = (target: Run, number: number) => (
    <button type="button" className="run-link-button" onClick={() => onSelect?.(target.id)}>
      {number > 0 ? `${t("runs.number", { number })} · ` : ""}
      {agentLabel(target)}
    </button>
  );
  const note = (text: string | undefined) =>
    text ? (
      <details className="run-handoff-note">
        <summary>{t("runs.handoffNote")}</summary>
        <pre>{text}</pre>
      </details>
    ) : null;
  return (
    <div className="run-handoff-panel">
      {waiting && (
        <div className="run-limit-wait-panel" role="status">
          <p>
            <b>{t("runs.limitWait", { time: formatLocalizedTime(waiting, language) })}</b>
          </p>
          <p>{t("runs.limitWaitHint", { time: formatLocalizedDateTime(waiting, language) })}</p>
          <div className="run-limit-wait-actions">
            <button
              type="button"
              className="run-retry-now"
              disabled={retrying || cancelling}
              onClick={() => void retryRunNow(run.id)}
            >
              {retrying ? t("runs.retrying") : t("runs.retryNow")}
            </button>
            <button
              type="button"
              className="run-cancel"
              disabled={retrying || cancelling}
              onClick={onCancel}
            >
              {cancelling ? t("runs.cancelling") : t("runs.cancel")}
            </button>
          </div>
        </div>
      )}
      {run.continuesFrom && (
        <div className="run-handoff-from">
          <p>
            {t("runs.continuesLead")}{" "}
            {prev ? link(prev, prevNumber) : <span className="mono">{run.continuesFrom}</span>}
          </p>
          {note(prev?.handoffNote)}
        </div>
      )}
      {run.handoffBranch && (
        <div className="run-handoff-to">
          <p>{t("runs.handoffKept", { branch: run.handoffBranch })}</p>
          {next && (
            <p>
              {t("runs.handedOffLead")} {link(next, nextNumber)}
            </p>
          )}
          {note(run.handoffNote)}
        </div>
      )}
    </div>
  );
}

function RunRow({
  run,
  number,
  selected,
  onSelect,
  retry = null,
  next = null,
}: {
  run: Run;
  /** 1 for the node's first run; 0 when unknown. */
  number: number;
  selected: boolean;
  onSelect: () => void;
  retry?: RetryInfo | null;
  next?: Run | null;
}) {
  const { t, language } = useI18n();
  const branch = run.branch || run.base;
  // The PR chip is a link, which cannot live inside the row's button: it
  // sits in a footer that reads as part of the same row.
  const summary = <RunChangesSummary run={run} className="run-row-changes" />;
  const hasSummary = runHasChanges(run) || Boolean(run.prUrl) || Boolean(run.prError);
  return (
    <li className={`run-item${selected ? " selected" : ""}${hasSummary ? " has-changes" : ""}`}>
      <button
        type="button"
        className={`run-row${selected ? " selected" : ""}`}
        aria-pressed={selected}
        onClick={onSelect}
      >
        <span className="run-row-top">
          <RunStatusBadge status={run.status} run={run} />
          <strong className="run-row-agent">
            {number > 0 && (
              <span className="run-row-number">
                <span className="visually-hidden">{t("runs.runWord")} </span>
                {t("runs.number", { number })}{" "}
              </span>
            )}
            {run.agent || (run.kind === "verify" ? t("runs.testCommand") : "")}
            {run.model ? ` · ${run.model}` : ""}
          </strong>
          <time
            dateTime={run.createdAt}
            title={formatLocalizedDateTime(run.createdAt, language)}
          >
            {relativeTime(run.createdAt)}
          </time>
        </span>
        <RunTags run={run} retry={retry} next={next} />
        <span className="run-row-meta">
          <span>{runnerDisplayName(t, run.runnerName) || run.runnerId}</span>
          <span>{run.repoName || run.repoId}</span>
          {branch && <span className="mono">{branch}</span>}
          <UsageText run={run} />
        </span>
      </button>
      {run.verify && (
        <span className="run-row-evidence">
          <VerifyEvidence verify={run.verify} />
        </span>
      )}
      {hasSummary && summary}
      <OfferAskCard run={run} className="run-row-ask" />
    </li>
  );
}

export function RunDetail({
  run,
  retry = null,
  prev = null,
  next = null,
  prevNumber = 0,
  nextNumber = 0,
  onSelect,
}: {
  run: Run;
  retry?: RetryInfo | null;
  /** The run this one retries, and the one that retries it, when listed. */
  prev?: Run | null;
  next?: Run | null;
  prevNumber?: number;
  nextNumber?: number;
  onSelect?: (runId: string) => void;
}) {
  const { t, language } = useI18n();
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const stopRequested = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelRun = useRuns((s) => s.cancelRun);
  const final = runFinal(run.status);
  const runners = useRunners((s) => s.runners);
  const viewer = useViewer();
  // Its agent is on the runner but signed out: how to sign in, to whoever
  // can (signedOut.ts), above what the CLI said.
  const signedOut = runSignedOut(run) ? signedOutText(t, run, signInReader(run, runners, viewer)) : "";

  const onCancel = async () => {
    if (await confirmCancelRun(t, run)) await cancelRun(run.id);
  };

  const single = useEdition() === "single";
  const simple = useSimpleMode();
  const hint = runStatusHint(t, run, single);
  const fix = single && run.status === "declined" ? declineFix(run.declineCode) : null;
  const [diffOpen, setDiffOpen] = useState(false);
  const closeDiff = useCallback(() => setDiffOpen(false), []);

  return (
    <section className="run-detail" aria-label={t("runs.detail")}>
      <div className="run-detail-head">
        <RunStatusBadge status={run.status} run={run} />
        <div className="run-detail-title">
          <strong>
            {run.agent}
            {run.model ? ` · ${run.model}` : ""}
          </strong>
          <RunTags run={run} retry={retry} next={next} />
          {stopRequested ? (
            <span>{t("runs.stopRequested")}</span>
          ) : (
            hint && (
              <span>
                {hint}
                {fix && (
                  <>
                    {" "}
                    <button type="button" className="run-inline-link" onClick={() => openSettings("runners")}>
                      {t(fix === "addRepo" ? "repoAdd.button" : "offerAsk.fixLocal")}
                    </button>
                  </>
                )}
              </span>
            )
          )}
        </div>
        <button
          type="button"
          className="run-workspace-open"
          title={t("runWorkspace.openTitle")}
          onClick={() => openRunWorkspace(run.id)}
        >
          <IconFolder size={13} />
          <span>{t("runWorkspace.open")}</span>
        </button>
        {runCancellable(run.status) && !stopRequested && !runWaitsForLimit(run) && (
          <button
            type="button"
            className="run-cancel"
            disabled={cancelling}
            onClick={() => void onCancel()}
          >
            {cancelling ? t("runs.cancelling") : t("runs.cancel")}
          </button>
        )}
      </div>
      <MergeControl run={run} className="run-detail-merge" />
      {run.contestId && run.contestLabel !== "J" && (
        <div className="run-detail-contest">
          <ContestDecide contestId={run.contestId} simple={simple} />
        </div>
      )}
      {run.override && <p className="run-override">{t("runs.override", { reason: run.override })}</p>}
      {run.checks && run.checks.length > 0 && <JudgeChecks checks={run.checks} />}
      <RunHandoff
        run={run}
        prev={prev}
        next={next}
        prevNumber={prevNumber}
        nextNumber={nextNumber}
        onSelect={onSelect}
        onCancel={() => void onCancel()}
      />

      {(runHasChanges(run) || run.prUrl || run.prError) && (
        <div className="run-detail-changes">
          <RunChangesSummary run={run} />
          {final && runHasChanges(run) && (
            <button type="button" className="run-link-button" onClick={() => setDiffOpen(true)}>
              {t("runs.viewChanges")}
            </button>
          )}
        </div>
      )}
      {diffOpen && <RunDiffDialog run={run} onClose={closeDiff} />}

      <dl className="run-meta">
        {/* Single-user: there is one machine, this computer. */}
        {!single && (
          <div>
            <dt>{t("runs.runner")}</dt>
            <dd>{runnerDisplayName(t, run.runnerName) || run.runnerId}</dd>
          </div>
        )}
        <div>
          <dt>{t("runs.repo")}</dt>
          <dd>{run.repoName || run.repoId}</dd>
        </div>
        {(run.branch || run.base) && (
          <div>
            <dt>{t("runs.branch")}</dt>
            <dd className="mono">
              {run.branch || run.base}
              {run.branch && run.base ? ` ← ${run.base}` : ""}
            </dd>
          </div>
        )}
        <div>
          <dt>{t("runs.created")}</dt>
          <dd>
            {formatLocalizedDateTime(run.createdAt, language)}
            {run.createdByName ? ` · ${run.createdByName}` : ""}
          </dd>
        </div>
        {run.finishedAt && (
          <div>
            <dt>{t("runs.finished")}</dt>
            <dd>{formatLocalizedDateTime(run.finishedAt, language)}</dd>
          </div>
        )}
        {(totalTokens(run.usage) > 0 || formatCost(run.usage?.costUsd)) && (
          <div>
            <dt>{t("runs.usage")}</dt>
            <dd>
              <UsageText run={run} />
            </dd>
          </div>
        )}
      </dl>

      {run.prompt && (
        <details className="run-prompt">
          <summary>{t("runs.prompt")}</summary>
          <p>{run.prompt}</p>
        </details>
      )}

      {cancelError && (
        <div className="notify-message error" role="alert">
          {cancelError}
        </div>
      )}

      {final && (run.summary || run.error) && (
        <div className={`run-outcome${run.error ? " is-error" : ""}`}>
          {run.summary && (
            <>
              <b>{t("runs.summary")}</b>
              <div className="run-ev-prose">
                {paragraphs(run.summary).map((part, index) => (
                  <p key={index}>{part}</p>
                ))}
              </div>
            </>
          )}
          {run.error && (
            <>
              <b>{t("runs.error")}</b>
              {signedOut && <p className="run-signed-out">{signedOut}</p>}
              <pre>{run.error}</pre>
            </>
          )}
          {run.status === "failed" && run.exitCode !== 0 && (
            <small>{t("runs.exitCode", { code: run.exitCode })}</small>
          )}
        </div>
      )}

      <RunModeTabs run={run}>
        <RunTimeline run={run} />
      </RunModeTabs>
    </section>
  );
}

const PIPELINE_STATUSES = new Set([
  "running",
  "waiting_human",
  "approved",
  "changes_left",
  "failed",
  "cancelled",
]);

export function PipelineStatusChip({ status }: { status: string }) {
  const { t } = useI18n();
  const known = PIPELINE_STATUSES.has(status);
  return (
    <span className={`run-pipeline-status is-${known ? status : "unknown"}`}>
      {known ? t(`pipeline.status.${status}`) : status}
    </span>
  );
}

/** "Pipeline · round 2/3 · implement claude/sonnet → review codex/gpt-5". */
export function pipelineHeaderText(
  t: (key: string, values?: Record<string, string | number>) => string,
  state: PipelineState | undefined,
  runs: Run[],
): string {
  const parts = [t("pipeline.title")];
  // A staged pipeline: its preset and where it is.
  if (state?.stages) {
    parts.push(t(`stages.preset.${state.stages.preset}`));
    if (state.phase) parts.push(t(`pipeline.phase.${state.phase}`));
  }
  if (state && state.round > 0) {
    parts.push(
      t("pipeline.round", { round: state.round, max: state.pipeline?.maxRounds || state.round }),
    );
  }
  // Until the pipeline is fetched, its runs say who implements and reviews.
  const implement =
    state?.pipeline?.implement ?? runs.find((run) => !run.kind || run.kind === "revise");
  const review = state?.pipeline?.review ?? runs.find((run) => run.kind === "review");
  const name = (step: { agent: string; model: string } | undefined) =>
    step ? (step.model ? `${step.agent}/${step.model}` : step.agent) : "";
  if (implement || review) {
    parts.push(
      t("pipeline.steps", { implement: name(implement) || "—", review: name(review) || "—" }),
    );
  }
  return parts.join(" · ");
}

function PipelineGroup({ id, children, runs }: { id: string; children: React.ReactNode; runs: Run[] }) {
  const { t } = useI18n();
  const state = useRuns((s) => s.pipelines[id]);
  const error = useRuns((s) => s.pipelineErrors[id]);
  const loadPipeline = useRuns((s) => s.loadPipeline);
  useEffect(() => {
    void loadPipeline(id);
  }, [id, loadPipeline]);
  const text = pipelineHeaderText(t, state, runs);
  return (
    <li className="run-pipeline" data-pipeline-id={id}>
      <div className="run-pipeline-head" role="group" aria-label={text}>
        <IconFork size={13} />
        <span className="run-pipeline-text">{text}</span>
        {state ? (
          <PipelineStatusChip status={state.status} />
        ) : (
          error && <span className="run-pipeline-error" title={error}>{t("pipeline.unknown")}</span>
        )}
      </div>
      {state?.guard && <GuardView guard={state.guard} />}
      <ul className="run-list run-pipeline-runs">{children}</ul>
    </li>
  );
}

type ListItem =
  | { type: "run"; run: Run; index: number }
  | { type: "pipeline"; id: string; entries: { run: Run; index: number }[] };

/** The list as shown: a pipeline's runs gathered under one header, placed
 * where its newest run sits; every other run on its own. */
export function groupRuns(list: Run[]): ListItem[] {
  const items: ListItem[] = [];
  const groups = new Map<string, Extract<ListItem, { type: "pipeline" }>>();
  list.forEach((run, index) => {
    const id = run.pipelineId;
    if (!id) {
      items.push({ type: "run", run, index });
      return;
    }
    let group = groups.get(id);
    if (!group) {
      group = { type: "pipeline", id, entries: [] };
      groups.set(id, group);
      items.push(group);
    }
    group.entries.push({ run, index });
  });
  return items;
}

/**
 * A node's runs: the list, newest first, and the selected one live. It lives
 * in the node's detail view (the Drawer's Runs tab).
 */
export function NodeRuns({ id }: { id: string }) {
  const { t } = useI18n();
  const runs = useRuns((s) => s.runsByNode[id]);
  const loading = useRuns((s) => Boolean(s.loadingNodes[id]));
  const error = useRuns((s) => s.nodeErrors[id]);
  const selectedId = useRuns((s) => s.selectedRun[id]);
  const loadRuns = useRuns((s) => s.loadRuns);
  const selectRun = useRuns((s) => s.selectRun);
  const openDispatch = useRuns((s) => s.openDispatch);
  // Only a type an agent may work gets a "new run" button; a Goal's runs come
  // from Autopilot and a Review's from nobody.
  const canDispatch = useNodeType({ nodeId: id }).agentMode === "dispatch";

  useEffect(() => {
    void loadRuns(id);
  }, [id, loadRuns]);

  const list = useMemo(() => runs ?? [], [runs]);
  const selected = list.find((run) => run.id === selectedId) ?? list[0];
  const items = useMemo(() => groupRuns(list), [list]);
  // The hub lists at most 50 runs: past that, counting from the oldest one
  // shown would number them wrong.
  const numberAt = (index: number) => (list.length < RUN_LIST_CAP ? list.length - index : 0);
  const retryOf = (run: Run): RetryInfo | null => {
    if (!run.retryOf) return null;
    const continues = Boolean(run.continuesFrom);
    const index = list.findIndex((item) => item.id === run.retryOf);
    if (index === -1) return { number: 0, continues };
    return { number: numberAt(index), failureKind: list[index].failureKind, continues };
  };
  const nextOf = (run: Run): Run | null => list.find((item) => item.retryOf === run.id) ?? null;
  const prevOf = (run: Run): Run | null =>
    run.retryOf ? (list.find((item) => item.id === run.retryOf) ?? null) : null;
  const numberOf = (run: Run | null) => (run ? numberAt(list.indexOf(run)) : 0);
  const row = (run: Run, index: number) => (
    <RunRow
      key={run.id}
      run={run}
      number={numberAt(index)}
      selected={run.id === selected?.id}
      onSelect={() => selectRun(id, run.id)}
      retry={retryOf(run)}
      next={nextOf(run)}
    />
  );

  return (
    <div className="node-runs">
      <div className="node-runs-head">
        <h3>
          <IconTerminal size={14} />
          {t("runs.title")}
          {list.length > 0 && <span className="node-runs-count">{list.length}</span>}
        </h3>
        {canDispatch && (
          <button type="button" className="run-dispatch-button" onClick={() => openDispatch(id)}>
            <IconSend size={13} />
            <span>{t("runs.newRun")}</span>
          </button>
        )}
      </div>

      {error && (
        <div className="notify-message error" role="status">
          {error}
        </div>
      )}

      {runs === undefined ? (
        <p className="run-empty">{loading ? t("common.loading") : ""}</p>
      ) : list.length === 0 ? (
        <p className="run-empty">{t("runs.empty")}</p>
      ) : (
        <>
          <ul className="run-list" aria-label={t("runs.title")}>
            {items.map((item) =>
              item.type === "run" ? (
                row(item.run, item.index)
              ) : (
                <PipelineGroup
                  key={`pipeline-${item.id}`}
                  id={item.id}
                  runs={item.entries.map((entry) => entry.run)}
                >
                  {item.entries.map((entry) => row(entry.run, entry.index))}
                </PipelineGroup>
              ),
            )}
          </ul>
          {selected && (
            <RunDetail
              key={selected.id}
              run={selected}
              retry={retryOf(selected)}
              prev={prevOf(selected)}
              next={nextOf(selected)}
              prevNumber={numberOf(prevOf(selected))}
              nextNumber={numberOf(nextOf(selected))}
              onSelect={(runId) => selectRun(id, runId)}
            />
          )}
        </>
      )}

      <NodeSessions id={id} />
    </div>
  );
}
