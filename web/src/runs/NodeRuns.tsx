import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { runCancellable, runFinal, type PipelineState, type Run, type RunEvent } from "../api";
import { confirmCancelRun } from "./confirmCancelRun";
import { IconFork, IconSend, IconTerminal } from "../icons";

import { formatLocalizedDateTime, formatLocalizedTime, useI18n } from "../i18n";
import { relativeTime } from "../runners/RunnersModal";
import {
  formatCompact,
  formatCost,
  formatCount,
  oneLine,
  paragraphs,
  prettyInput,
  runHasChanges,
  totalTokens,
} from "./runFormat";
import { RunDiffDialog } from "./DiffViewer";
import { MergeControl } from "./MergeApproval";
import { NodeSessions } from "./NodeSessions";
import { RunChangesSummary } from "./RunChanges";
import { RunModeTabs } from "./RunModeTabs";
import { useRuns } from "./runStore";
import { useNodeType } from "../nodeTypeTheme";

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

export function RunStatusBadge({ status }: { status: string }) {
  const { t } = useI18n();
  const known = KNOWN_STATUSES.has(status);
  return (
    <span className={`run-status run-status-${known ? status : "unknown"}`}>
      {known ? t(`runs.status.${status}`) : status}
    </span>
  );
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

/** Whether a run waits, queued, for its agent's usage limit to reset. */
export function runWaitsForLimit(run: Run): boolean {
  return run.status === "queued" && Boolean(run.retryAt);
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

export function RunKindBadge({ kind }: { kind?: string }) {
  const { t } = useI18n();
  if (kind !== "review" && kind !== "revise") return null;
  return <span className={`run-kind run-kind-${kind}`}>{t(`runs.kind.${kind}`)}</span>;
}

export function VerdictChip({ verdict }: { verdict?: string }) {
  const { t } = useI18n();
  if (verdict !== "approve" && verdict !== "changes") return null;
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
  const kind = run.kind === "review" || run.kind === "revise";
  const verdict = run.verdict === "approve" || run.verdict === "changes";
  const waiting = runWaitsForLimit(run);
  const handedTo = run.handoffBranch && next ? next : null;
  if (!kind && !verdict && !failure && !retry && !waiting && !handedTo) return null;
  return (
    <span className="run-row-tags">
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
          <RunStatusBadge status={run.status} />
          <strong className="run-row-agent">
            {number > 0 && (
              <span className="run-row-number">{t("runs.number", { number })} </span>
            )}
            {run.agent}
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
          <span>{run.runnerName || run.runnerId}</span>
          <span>{run.repoName || run.repoId}</span>
          {branch && <span className="mono">{branch}</span>}
          <UsageText run={run} />
        </span>
      </button>
      {hasSummary && summary}
    </li>
  );
}

function Collapsible({
  className,
  summary,
  children,
  defaultOpen = false,
}: {
  className: string;
  summary: React.ReactNode;
  children: React.ReactNode;
  defaultOpen?: boolean;
}) {
  return (
    <details className={className} open={defaultOpen || undefined}>
      <summary>{summary}</summary>
      {children}
    </details>
  );
}

const HUB_PREFIX = "hub: ";

export function RunEventItem({
  event,
  toolNames,
}: {
  event: RunEvent;
  /** Tool name by tool id, so a result can say what it answers. */
  toolNames?: Map<string, string>;
}) {
  const { t } = useI18n();
  const text = event.text ?? "";
  switch (event.type) {
    case "text":
      return (
        <li className="run-ev run-ev-text" data-seq={event.seq}>
          {paragraphs(text).map((part, index) => (
            <p key={index}>{part}</p>
          ))}
        </li>
      );
    case "thinking":
      return (
        <li className="run-ev run-ev-thinking" data-seq={event.seq}>
          <Collapsible
            className="run-ev-fold"
            summary={
              <>
                <b>{t("runs.thinking")}</b>
                <span className="run-ev-preview">{oneLine(text)}</span>
              </>
            }
          >
            <div className="run-ev-prose">
              {paragraphs(text).map((part, index) => (
                <p key={index}>{part}</p>
              ))}
            </div>
          </Collapsible>
        </li>
      );
    case "tool_call": {
      const input = event.input ?? "";
      return (
        <li className="run-ev run-ev-tool" data-seq={event.seq}>
          <Collapsible
            className="run-ev-fold"
            summary={
              <>
                <b className="mono">{event.tool || t("runs.tool")}</b>
                {input && <span className="run-ev-preview mono">{oneLine(input)}</span>}
              </>
            }
          >
            {input ? <pre>{prettyInput(input)}</pre> : <p className="run-ev-none">{t("runs.noInput")}</p>}
          </Collapsible>
        </li>
      );
    }
    case "tool_result": {
      const tool = event.toolId ? toolNames?.get(event.toolId) : undefined;
      const label = event.isError ? t("runs.toolError") : t("runs.toolResult");
      return (
        <li
          className={`run-ev run-ev-result${event.isError ? " is-error" : ""}`}
          data-seq={event.seq}
        >
          <Collapsible
            className="run-ev-fold"
            summary={
              <>
                <b>{tool ? `${label} · ${tool}` : label}</b>
                {text && <span className="run-ev-preview mono">{oneLine(text)}</span>}
              </>
            }
          >
            {text ? <pre>{text}</pre> : <p className="run-ev-none">{t("runs.emptyResult")}</p>}
          </Collapsible>
        </li>
      );
    }
    case "usage": {
      const usage = event.usage;
      if (!usage) return null;
      const parts = [
        t("runs.usageIn", { count: formatCount(usage.inputTokens ?? 0) }),
        t("runs.usageOut", { count: formatCount(usage.outputTokens ?? 0) }),
      ];
      if (usage.cacheReadTokens) {
        parts.push(t("runs.usageCacheRead", { count: formatCount(usage.cacheReadTokens) }));
      }
      if (usage.cacheWriteTokens) {
        parts.push(t("runs.usageCacheWrite", { count: formatCount(usage.cacheWriteTokens) }));
      }
      const cost = formatCost(usage.costUsd);
      if (cost) parts.push(cost);
      return (
        <li className="run-ev run-ev-usage" data-seq={event.seq}>
          <b>{t("runs.usage")}</b> {parts.join(" · ")}
        </li>
      );
    }
    case "error":
      return (
        <li className="run-ev run-ev-error" data-seq={event.seq} role="alert">
          <b>{t("runs.error")}</b>
          <pre>{text}</pre>
        </li>
      );
    default:
      // The hub's own notes on a run (W5: a fallback taken, a chain used up,
      // a pipeline's end) arrive as log lines prefixed "hub: ".
      if (event.type === "log" && text.startsWith(HUB_PREFIX)) {
        return (
          <li className="run-ev run-ev-hub" data-seq={event.seq}>
            <b>{t("runs.hubNote")}</b> {text.slice(HUB_PREFIX.length)}
          </li>
        );
      }
      // "log", and anything a newer runner sends that this page predates.
      return (
        <li className="run-ev run-ev-log mono" data-seq={event.seq}>
          {text || event.type}
        </li>
      );
  }
}

/** Whether a scroll box is (nearly) scrolled to its end. */
function atEnd(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight < 24;
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
  const events = useRuns((s) => s.eventsByRun[run.id]);
  const eventError = useRuns((s) => s.eventErrors[run.id]);
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const stopRequested = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const watchRun = useRuns((s) => s.watchRun);
  const cancelRun = useRuns((s) => s.cancelRun);

  useEffect(() => watchRun(run.id), [run.id, watchRun]);

  // A finished run's last events can land after the status flips; one more
  // fetch on the flip picks them up without waiting for a push.
  const final = runFinal(run.status);
  const loadEvents = useRuns((s) => s.loadEvents);
  const wasFinal = useRef(final);
  useEffect(() => {
    if (final && !wasFinal.current) void loadEvents(run.id);
    wasFinal.current = final;
  }, [final, run.id, loadEvents]);

  const list = events ?? [];
  const toolNames = useMemo(() => {
    const names = new Map<string, string>();
    for (const event of list) {
      if (event.type === "tool_call" && event.toolId && event.tool) names.set(event.toolId, event.tool);
    }
    return names;
  }, [list]);

  // Follow the end of the timeline while the reader is there; once they
  // scroll up to read, stay put and offer a way back down.
  const scroller = useRef<HTMLOListElement>(null);
  const following = useRef(true);
  const [behind, setBehind] = useState(false);
  useLayoutEffect(() => {
    const element = scroller.current;
    if (!element) return;
    if (following.current) element.scrollTop = element.scrollHeight;
    else setBehind(true);
  }, [list.length]);

  const onCancel = async () => {
    if (await confirmCancelRun(t, run)) await cancelRun(run.id);
  };

  const hintKey = KNOWN_STATUSES.has(run.status) ? `runs.statusHint.${run.status}` : "";
  const [diffOpen, setDiffOpen] = useState(false);
  const closeDiff = useCallback(() => setDiffOpen(false), []);

  return (
    <section className="run-detail" aria-label={t("runs.detail")}>
      <div className="run-detail-head">
        <RunStatusBadge status={run.status} />
        <div className="run-detail-title">
          <strong>
            {run.agent}
            {run.model ? ` · ${run.model}` : ""}
          </strong>
          <RunTags run={run} retry={retry} next={next} />
          {stopRequested ? (
            <span>{t("runs.stopRequested")}</span>
          ) : (
            hintKey && <span>{t(hintKey)}</span>
          )}
        </div>
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
        <div>
          <dt>{t("runs.runner")}</dt>
          <dd>{run.runnerName || run.runnerId}</dd>
        </div>
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
              <pre>{run.error}</pre>
            </>
          )}
          {run.status === "failed" && run.exitCode !== 0 && (
            <small>{t("runs.exitCode", { code: run.exitCode })}</small>
          )}
        </div>
      )}

      <RunModeTabs run={run}>
      <div className="run-timeline-wrap">
        <ol
          ref={scroller}
          className="run-events"
          aria-label={t("runs.activity")}
          onScroll={(event) => {
            const end = atEnd(event.currentTarget);
            following.current = end;
            if (end) setBehind(false);
          }}
        >
          {list.length === 0 ? (
            <li className="run-ev run-ev-empty">
              {eventError ? "" : events === undefined ? t("common.loading") : t("runs.noEvents")}
            </li>
          ) : (
            list.map((event) => (
              <RunEventItem key={event.seq} event={event} toolNames={toolNames} />
            ))
          )}
        </ol>
        {behind && (
          <button
            type="button"
            className="run-jump"
            onClick={() => {
              const element = scroller.current;
              if (element) element.scrollTop = element.scrollHeight;
              following.current = true;
              setBehind(false);
            }}
          >
            {t("runs.jumpLatest")}
          </button>
        )}
      </div>
      {eventError && (
        <div className="notify-message error" role="status">
          {eventError}
        </div>
      )}
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
