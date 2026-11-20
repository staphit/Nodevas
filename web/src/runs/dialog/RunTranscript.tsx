import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { RunAsk } from "../../api";
import { useI18n } from "../../i18n";
import { prettyInput } from "../runFormat";
import { RunAskItem } from "../RunTimeline";
import { formatToolSummary } from "../toolLine";
import type { RunDialogLine } from "./runLines";

/** Initial number of transcript lines rendered; older lines sit behind "Show earlier". */
export const INITIAL_LINE_WINDOW = 200;

/** Minimum interval between screen-reader announcements of new agent text. */
export const ANNOUNCE_THROTTLE_MS = 5000;

/** Maximum characters read aloud per polite announcement. */
const ANNOUNCE_MAX_CHARS = 200;

type TextSegment = { kind: "prose"; text: string } | { kind: "code"; lang: string; code: string };

/** Splits agent text into pre-wrap prose and fenced monospace code blocks. */
function splitCodeFences(text: string): TextSegment[] {
  const segments: TextSegment[] = [];
  const pattern = /```([^\n`]*)\n([\s\S]*?)```/g;
  let last = 0;
  for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
    if (match.index > last) {
      segments.push({ kind: "prose", text: text.slice(last, match.index) });
    }
    segments.push({
      kind: "code",
      lang: match[1].trim(),
      code: match[2].replace(/\n$/, ""),
    });
    last = match.index + match[0].length;
  }
  if (last < text.length) {
    segments.push({ kind: "prose", text: text.slice(last) });
  }
  if (segments.length === 0) segments.push({ kind: "prose", text });
  return segments;
}

function atEnd(element: HTMLElement): boolean {
  return element.scrollHeight - element.scrollTop - element.clientHeight < 24;
}

export interface RunTranscriptProps {
  lines: readonly RunDialogLine[];
  simple?: boolean;
  /** Whether new agent text should be announced in the polite live region (default true). */
  live?: boolean;
  /** Flavour or canned empty line shown before the first event. */
  emptyLine?: ReactNode;
  /** Called when the user clicks "Take back" on a pending queued follow-up line. */
  onWithdraw?: (runId: string) => void;
  /** Optional inline AskCard renderer for pending node-run asks. */
  renderAsk?: (ask: RunAsk) => ReactNode;
  className?: string;
}

/**
 * Background run transcript (§3.1, §3.6). No animation; renders the last 200
 * lines with "Show earlier", follows the end while scrolled down, hides runner
 * logs behind "Show runner notes", and throttles polite live announcements of
 * agent text to once every 5 s.
 */
export function RunTranscript({
  lines,
  simple = false,
  live = true,
  emptyLine,
  onWithdraw,
  renderAsk,
  className,
}: RunTranscriptProps) {
  const { t } = useI18n();
  const [showNotes, setShowNotes] = useState(false);
  const [windowSize, setWindowSize] = useState(INITIAL_LINE_WINDOW);

  const hasHiddenNotes = useMemo(
    () => lines.some((line) => line.kind === "log" && line.hidden),
    [lines],
  );

  const filtered = useMemo(
    () => (showNotes ? lines : lines.filter((line) => !(line.kind === "log" && line.hidden))),
    [lines, showNotes],
  );

  const hasEarlier = filtered.length > windowSize;
  const visible = useMemo(
    () => (hasEarlier ? filtered.slice(filtered.length - windowSize) : filtered),
    [filtered, hasEarlier, windowSize],
  );

  // Follow the end while the reader is at the bottom; show "Jump to latest" otherwise.
  const scroller = useRef<HTMLOListElement>(null);
  const following = useRef(true);
  const [behind, setBehind] = useState(false);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (following.current) el.scrollTop = el.scrollHeight;
    else setBehind(true);
  }, [visible.length]);

  // Polite live region announcing new agent `text` only (first 200 chars, <= 1 per 5s).
  // Seed lastTextKeyRef with any pre-existing text on initial mount so opening
  // a historical transcript does not blurt out old lines.
  const latestTextKey = (list: readonly RunDialogLine[]): string | null => {
    for (let i = list.length - 1; i >= 0; i -= 1) {
      const item = list[i];
      if (item.kind === "text" && item.text.trim()) return item.key;
    }
    return null;
  };
  const [announcement, setAnnouncement] = useState("");
  const lastTextKeyRef = useRef<string | null>(latestTextKey(lines));
  const lastAnnouncedAtRef = useRef<number | null>(null);
  const pendingTextRef = useRef<string | null>(null);
  const announceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!live) return;
    let latestTextLine: Extract<RunDialogLine, { kind: "text" }> | null = null;
    for (let i = lines.length - 1; i >= 0; i -= 1) {
      const item = lines[i];
      if (item.kind === "text" && item.text.trim()) {
        latestTextLine = item;
        break;
      }
    }
    if (!latestTextLine || latestTextLine.key === lastTextKeyRef.current) return;
    lastTextKeyRef.current = latestTextLine.key;
    const clipped = latestTextLine.text.slice(0, ANNOUNCE_MAX_CHARS);

    const now = Date.now();
    const elapsed =
      lastAnnouncedAtRef.current === null ? ANNOUNCE_THROTTLE_MS : now - lastAnnouncedAtRef.current;

    if (elapsed >= ANNOUNCE_THROTTLE_MS) {
      if (announceTimerRef.current) {
        clearTimeout(announceTimerRef.current);
        announceTimerRef.current = null;
      }
      pendingTextRef.current = null;
      lastAnnouncedAtRef.current = now;
      setAnnouncement(clipped);
      return;
    }

    pendingTextRef.current = clipped;
    if (!announceTimerRef.current) {
      const wait = Math.max(0, ANNOUNCE_THROTTLE_MS - elapsed);
      announceTimerRef.current = setTimeout(() => {
        announceTimerRef.current = null;
        if (pendingTextRef.current !== null) {
          lastAnnouncedAtRef.current = Date.now();
          setAnnouncement(pendingTextRef.current);
          pendingTextRef.current = null;
        }
      }, wait);
    }
  }, [lines, live]);

  useEffect(
    () => () => {
      if (announceTimerRef.current) clearTimeout(announceTimerRef.current);
    },
    [],
  );

  const renderNoticeText = (notice: Extract<RunDialogLine, { kind: "notice" }>["notice"]) => {
    switch (notice) {
      case "branch_moved":
        return t("runDialog.err.branchMoved");
      case "quiet":
        return t(simple ? "runDialog.simple.end.quiet" : "runDialog.end.quiet");
      case "quiet_waiting":
        return t(simple ? "runDialog.simple.end.quietWaiting" : "runDialog.end.quietWaiting");
      case "resume_lost":
        return t("runDialog.resumeLost");
    }
  };

  return (
    <div className={className ? `run-dlg-transcript ${className}` : "run-dlg-transcript"}>
      {hasHiddenNotes && (
        <div className="run-dlg-transcript-bar">
          <label className="run-dlg-notes-toggle">
            <input
              type="checkbox"
              checked={showNotes}
              onChange={(event) => setShowNotes(event.target.checked)}
            />
            <span>{t("runDialog.notes")}</span>
          </label>
        </div>
      )}

      <div className="run-dlg-log-wrap">
        <ol
          ref={scroller}
          role="log"
          aria-live="off"
          className="run-dlg-log-list"
          onScroll={(event) => {
            const end = atEnd(event.currentTarget);
            following.current = end;
            if (end) setBehind(false);
          }}
        >
          {hasEarlier && (
            <li className="run-dlg-earlier">
              <button
                type="button"
                className="run-dlg-earlier-btn"
                onClick={() => setWindowSize((prev) => prev + INITIAL_LINE_WINDOW)}
              >
                {t("runDialog.earlier")}
              </button>
            </li>
          )}

          {visible.length === 0 ? (
            <li className="run-dlg-line run-dlg-empty">
              {emptyLine && <div className="run-dlg-empty-flavour">{emptyLine}</div>}
              <span>{t("runDialog.noOutput")}</span>
            </li>
          ) : (
            visible.map((line) => {
              switch (line.kind) {
                case "turn":
                  return (
                    <li key={line.key} className="run-dlg-line run-dlg-turn">
                      <span>{t("runDialog.turn", { n: line.index })}</span>
                    </li>
                  );
                case "user":
                  return (
                    <li
                      key={line.key}
                      className={`run-dlg-line run-dlg-user${line.pending ? " is-pending" : ""}`}
                    >
                      <span className="run-dlg-user-prefix" aria-hidden="true">
                        ›
                      </span>
                      <strong className="run-dlg-user-label">{t("runDialog.you")}:</strong>
                      <span className="run-dlg-user-text">{line.text}</span>
                      {line.pending && line.withdrawRunId && onWithdraw && (
                        <button
                          type="button"
                          className="run-dlg-withdraw"
                          onClick={() => onWithdraw(line.withdrawRunId!)}
                        >
                          {t("runDialog.withdraw")}
                        </button>
                      )}
                    </li>
                  );
                case "text": {
                  const segments = splitCodeFences(line.text);
                  return (
                    <li key={line.key} className="run-dlg-line run-dlg-text">
                      {segments.map((seg, idx) =>
                        seg.kind === "code" ? (
                          <pre key={idx} className="run-dlg-code" data-lang={seg.lang || undefined}>
                            <code>{seg.code}</code>
                          </pre>
                        ) : (
                          <span key={idx} className="run-dlg-prose">
                            {seg.text}
                          </span>
                        ),
                      )}
                      {line.truncated && <span className="run-dlg-cut"> {t("runDialog.cut")}</span>}
                    </li>
                  );
                }
                case "tool": {
                  const rawInput = line.call.input ?? "";
                  const rawOutput = line.result?.text ?? "";
                  return (
                    <li key={line.key} className={`run-dlg-line run-dlg-tool-row is-${line.summary.state}`}>
                      <details className="run-dlg-tool">
                        <summary>
                          <span className="run-dlg-tool-prefix" aria-hidden="true">
                            ›
                          </span>
                          <span className="run-dlg-tool-label">{formatToolSummary(t, line.summary)}</span>
                          {line.summary.state === "running" && (
                            <span
                              className="run-dlg-tool-state is-running"
                              aria-label={t("runDialog.tool.running")}
                            >
                              …
                            </span>
                          )}
                          {line.summary.state === "failed" && (
                            <span
                              className="run-dlg-tool-state is-failed"
                              aria-label={t("runDialog.tool.failed")}
                            >
                              ✗
                            </span>
                          )}
                        </summary>
                        <div className="run-dlg-tool-detail">
                          {rawInput && <pre className="run-dlg-tool-input">{prettyInput(rawInput)}</pre>}
                          {rawOutput && <pre className="run-dlg-tool-output">{rawOutput}</pre>}
                        </div>
                      </details>
                    </li>
                  );
                }
                case "thinking":
                  return (
                    <li key={line.key} className="run-dlg-line run-dlg-thinking-row">
                      <details className="run-dlg-thinking">
                        <summary>{t("runDialog.tool.thinking")}</summary>
                        <div className="run-dlg-thinking-body">{line.text}</div>
                      </details>
                    </li>
                  );
                case "error":
                  return (
                    <li key={line.key} className="run-dlg-line run-dlg-error" role="alert">
                      {line.text}
                    </li>
                  );
                case "log":
                  return (
                    <li
                      key={line.key}
                      className={`run-dlg-line run-dlg-log${line.hubNote ? " is-hub" : ""}${
                        line.exitNote ? " is-exit" : ""
                      }`}
                    >
                      {line.text}
                    </li>
                  );
                case "ask":
                  if (renderAsk && line.ask.status === "pending") {
                    return (
                      <li key={line.key} className="run-dlg-line run-dlg-ask">
                        {renderAsk(line.ask)}
                      </li>
                    );
                  }
                  return <RunAskItem key={line.key} ask={line.ask} />;
                case "notice":
                  return (
                    <li key={line.key} className="run-dlg-line run-dlg-notice" role="status">
                      {renderNoticeText(line.notice)}
                    </li>
                  );
              }
            })
          )}
        </ol>

        {behind && (
          <button
            type="button"
            className="run-jump run-dlg-jump"
            onClick={() => {
              const el = scroller.current;
              if (el) el.scrollTop = el.scrollHeight;
              following.current = true;
              setBehind(false);
            }}
          >
            {t("runs.jumpLatest")}
          </button>
        )}
      </div>

      <div className="sr-only" aria-live="polite" aria-atomic="true">
        {announcement}
      </div>
    </div>
  );
}
