import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { runCancellable, runFinal, type Run, type RunEvent } from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { IconSend, IconTerminal } from "../icons";
import { formatLocalizedDateTime, useI18n } from "../i18n";
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
import { RunChangesSummary } from "./RunChanges";
import { useRuns } from "./runStore";

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

function RunRow({
  run,
  number,
  selected,
  onSelect,
}: {
  run: Run;
  /** 1 for the node's first run; 0 when unknown. */
  number: number;
  selected: boolean;
  onSelect: () => void;
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

export function RunDetail({ run }: { run: Run }) {
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
    const confirmed = await confirmAction({
      title: t("runs.cancelTitle"),
      description: t(run.status === "running" ? "runs.cancelRunning" : "runs.cancelPending"),
      confirmLabel: t("runs.cancel"),
      cancelLabel: t("runs.keep"),
      tone: "danger",
    });
    if (confirmed) await cancelRun(run.id);
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
          {stopRequested ? (
            <span>{t("runs.stopRequested")}</span>
          ) : (
            hintKey && <span>{t(hintKey)}</span>
          )}
        </div>
        {runCancellable(run.status) && !stopRequested && (
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
    </section>
  );
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

  useEffect(() => {
    void loadRuns(id);
  }, [id, loadRuns]);

  const list = runs ?? [];
  const selected = list.find((run) => run.id === selectedId) ?? list[0];

  return (
    <div className="node-runs">
      <div className="node-runs-head">
        <h3>
          <IconTerminal size={14} />
          {t("runs.title")}
          {list.length > 0 && <span className="node-runs-count">{list.length}</span>}
        </h3>
        <button type="button" className="run-dispatch-button" onClick={() => openDispatch(id)}>
          <IconSend size={13} />
          <span>{t("runs.newRun")}</span>
        </button>
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
            {list.map((run, index) => (
              <RunRow
                key={run.id}
                run={run}
                // The hub lists at most 50 runs: past that, counting from the
                // oldest one shown would number them wrong.
                number={list.length < RUN_LIST_CAP ? list.length - index : 0}
                selected={run.id === selected?.id}
                onSelect={() => selectRun(id, run.id)}
              />
            ))}
          </ul>
          {selected && <RunDetail key={selected.id} run={selected} />}
        </>
      )}
    </div>
  );
}
