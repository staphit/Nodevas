import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { runFinal, type Run, type RunAsk, type RunEvent } from "../api";
import { useAgents } from "../agents/agentStore";
import { useI18n } from "../i18n";
import { usePreference } from "../store";
import { formatCost, formatCount, oneLine, paragraphs, prettyInput } from "./runFormat";
import { askOutcome, askSubject, interleaveAsks } from "./runAsks";
import { useRuns } from "./runStore";

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
    case "user":
      return (
        <li className="run-ev run-ev-user" data-seq={event.seq}>
          <b>{t("runDialog.you")}</b>
          {paragraphs(text).map((part, index) => (
            <p key={index}>{part}</p>
          ))}
        </li>
      );
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

/** One of the run's asks in its timeline: what it waits on, or how that
 * ended. Waiting on the viewer, it opens the agent panel, where its card is
 * answered. */
export function RunAskItem({ ask }: { ask: RunAsk }) {
  const { t } = useI18n();
  // Station docks Agent chat (and lists what waits on you) beside the run
  // view, with no floating panel to open from here.
  const station = usePreference("projectMode") === "station";
  const what = askSubject(t, ask);
  if (ask.status === "pending") {
    const text = t(ask.canAnswer ? "runs.ask.waitingYou" : "runs.ask.waiting", { what });
    return (
      <li className="run-ev run-ev-ask is-pending">
        {ask.canAnswer && !station ? (
          <button
            type="button"
            className="run-ev-ask-open"
            title={t("runs.ask.open")}
            onClick={() => useAgents.getState().setOpen(true)}
          >
            {text}
          </button>
        ) : (
          text
        )}
      </li>
    );
  }
  return (
    <li className={`run-ev run-ev-ask is-${ask.decision || ask.status}`}>
      {t("runs.ask.settled", { what, outcome: askOutcome(t, ask) })}
    </li>
  );
}

/** Whether a scroll box is (nearly) scrolled to its end. */
function atEnd(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight < 24;
}

/**
 * A run's activity: its events and asks in order, followed live while the
 * view is up. It keeps to the end while the reader is there; once they scroll
 * up to read, it stays put and offers a way back down. The run view and the
 * agent workspace's Log tab both show it.
 */
export function RunTimeline({ run }: { run: Run }) {
  const { t } = useI18n();
  const events = useRuns((s) => s.eventsByRun[run.id]);
  const asks = useRuns((s) => s.asksByRun[run.id]);
  const eventError = useRuns((s) => s.eventErrors[run.id]);
  const watchRun = useRuns((s) => s.watchRun);
  const loadEvents = useRuns((s) => s.loadEvents);

  useEffect(() => watchRun(run.id), [run.id, watchRun]);

  // A finished run's last events can land after the status flips; one more
  // fetch on the flip picks them up without waiting for a push.
  const final = runFinal(run.status);
  const wasFinal = useRef(final);
  useEffect(() => {
    if (final && !wasFinal.current) void loadEvents(run.id);
    wasFinal.current = final;
  }, [final, run.id, loadEvents]);

  const list = useMemo(() => events ?? [], [events]);
  const items = useMemo(() => interleaveAsks(list, asks), [list, asks]);
  const toolNames = useMemo(() => {
    const names = new Map<string, string>();
    for (const event of list) {
      if (event.type === "tool_call" && event.toolId && event.tool) names.set(event.toolId, event.tool);
    }
    return names;
  }, [list]);

  const scroller = useRef<HTMLOListElement>(null);
  const following = useRef(true);
  const [behind, setBehind] = useState(false);
  useLayoutEffect(() => {
    const element = scroller.current;
    if (!element) return;
    if (following.current) element.scrollTop = element.scrollHeight;
    else setBehind(true);
  }, [items.length]);

  return (
    <>
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
          {items.length === 0 ? (
            <li className="run-ev run-ev-empty">
              {eventError ? "" : events === undefined ? t("common.loading") : t("runs.noEvents")}
            </li>
          ) : (
            items.map((item) =>
              item.kind === "ask" ? (
                <RunAskItem key={`ask-${item.ask.id}`} ask={item.ask} />
              ) : (
                <RunEventItem key={item.event.seq} event={item.event} toolNames={toolNames} />
              ),
            )
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
    </>
  );
}
