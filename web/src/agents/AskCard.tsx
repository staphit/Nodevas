import { useEffect, useId, useState, type KeyboardEvent } from "react";
import type { AgentAsk, AgentAskAnswerRequest } from "../api";
import { useI18n } from "../i18n";

export function askQuestionText(
  t: (key: string, values?: Record<string, string | number>) => string,
  ask: Pick<AgentAsk, "kind" | "question" | "agent" | "sessionName" | "sessionId" | "tool">,
  fallbackAgent?: string,
): string {
  if (ask.kind === "permission") {
    const agent = ask.sessionName || ask.agent || ask.sessionId || fallbackAgent || "agent";
    return t("agents.ask.permissionQuestion", {
      agent,
      tool: ask.tool || "tool",
    });
  }
  return ask.question;
}

/** The hub, runner and MCP server each cut a permission prompt's input to
 * this many bytes, and say nothing when they do. */
export const TOOL_INPUT_MAX_BYTES = 4096;

/**
 * Whether a permission prompt's input may have been cut: it reaches the size
 * limit (the cut backs off up to 3 bytes to a rune boundary), or it is JSON
 * that ends early (cut, then trimmed of the blank padding before the cut).
 */
export function toolInputMayBeCut(input: string | undefined): boolean {
  if (!input) return false;
  if (new TextEncoder().encode(input).length >= TOOL_INPUT_MAX_BYTES - 3) return true;
  const trimmed = input.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      JSON.parse(trimmed);
    } catch {
      return true;
    }
  }
  return false;
}

function ToolInput({
  input,
  t,
}: {
  input: string;
  t: (key: string, values?: Record<string, string | number>) => string;
}) {
  // A run of 16 blank characters or more is drawn as a visible marker, so
  // padding cannot push the rest of a command out of sight.
  const parts: Array<string | { blank: number }> = [];
  const pattern = /\s{16,}/g;
  let last = 0;
  for (let match = pattern.exec(input); match; match = pattern.exec(input)) {
    if (match.index > last) parts.push(input.slice(last, match.index));
    parts.push({ blank: match[0].length });
    last = match.index + match[0].length;
  }
  if (last < input.length) parts.push(input.slice(last));
  return (
    <pre>
      {parts.map((part, i) =>
        typeof part === "string" ? (
          part
        ) : (
          <mark key={i} className="agent-ask-blank-run">
            {t("agents.ask.blankRun", { count: part.blank })}
          </mark>
        ),
      )}
    </pre>
  );
}

export function AskCard({
  ask,
  submitting,
  canAnswer = true,
  onSelectNode,
  onAnswer,
}: {
  ask: AgentAsk;
  submitting?: boolean;
  canAnswer?: boolean;
  onSelectNode?: (nodeId: string) => void;
  onAnswer: (payload: AgentAskAnswerRequest) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const [note, setNote] = useState("");
  const [answerText, setAnswerText] = useState("");
  const [now, setNow] = useState(() => Date.now());
  const busy = Boolean(submitting);

  const kind = ask.kind ?? "approval";
  const isPending = ask.status === "pending";
  const inputCut = kind === "permission" && toolInputMayBeCut(ask.toolInput);
  // The hub remembers "allow for this run" per run: outside one it would
  // allow this prompt alone, while the card claimed more.
  const canAllowRun = Boolean(ask.runId) && !inputCut;

  useEffect(() => {
    if (!isPending) return;
    const interval = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(interval);
  }, [isPending]);

  const timeoutMs = kind === "permission" ? 5 * 60 * 1000 : 10 * 60 * 1000;
  const expiresAt = ask.expiresAt ?? ask.createdAt + timeoutMs;
  const remainingMs = Math.max(0, expiresAt - now);
  const remainingSec = Math.floor(remainingMs / 1000);
  const minutes = Math.floor(remainingSec / 60);
  const seconds = remainingSec % 60;
  const formattedCountdown = `${minutes}:${seconds < 10 ? "0" : ""}${seconds}`;

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey || event.altKey || busy || !canAnswer || !isPending) return;
    const targetTag = (event.target as HTMLElement)?.tagName;
    if (targetTag === "INPUT" || targetTag === "TEXTAREA") return;

    if (kind === "approval") {
      if (event.key === "1") {
        event.preventDefault();
        onAnswer({ decision: "approve", ...(note ? { note } : {}) });
      } else if (event.key === "2") {
        event.preventDefault();
        onAnswer({ decision: "deny", ...(note ? { note } : {}) });
      }
    } else if (kind === "permission") {
      // A cut input is allowed with a deliberate click, never a stray key.
      if (inputCut && (event.key === "1" || event.key === "2")) return;
      if (event.key === "2" && !canAllowRun) return;
      if (event.key === "1") {
        event.preventDefault();
        onAnswer({ decision: "approve" });
      } else if (event.key === "2") {
        event.preventDefault();
        onAnswer({ decision: "approve_run" });
      } else if (event.key === "3") {
        event.preventDefault();
        onAnswer({ decision: "deny" });
      }
    }
  };

  const getTitle = () => {
    const session = ask.sessionName || ask.sessionId;
    switch (kind) {
      case "question":
        return t("agents.ask.questionFrom", { session });
      case "choice":
        return t("agents.ask.choiceFrom", { session });
      case "permission":
        return t("agents.ask.permissionFrom", { session });
      case "approval":
      default:
        return t("agents.ask.from", { session });
    }
  };

  return (
    <div
      className={`copilot-permission agent-ask-card agent-ask-kind-${kind}`}
      role="group"
      aria-labelledby={titleId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      data-ask-id={ask.id}
    >
      <div className="copilot-permission-head">
        <span id={titleId} className="copilot-permission-title" title={getTitle()}>
          {getTitle()}
        </span>
        {ask.agent && <span className="copilot-badge">{ask.agent}</span>}
        {kind === "permission" && ask.tool && <span className="copilot-badge">{ask.tool}</span>}
        {ask.runId && (
          <span className="agent-node-chip" title={t("agents.ask.run", { runId: ask.runId })}>
            {t("agents.ask.run", { runId: ask.runId.slice(0, 8) })}
          </span>
        )}
        {ask.nodeId && (
          <button
            type="button"
            className="agent-node-chip"
            onClick={() => onSelectNode?.(ask.nodeId!)}
          >
            #{ask.nodeId}
          </button>
        )}
        {isPending && remainingMs > 0 && (
          <span className="copilot-badge agent-ask-countdown">
            {t("agents.ask.expiresIn", { time: formattedCountdown })}
          </span>
        )}
      </div>

      <p className="copilot-permission-summary">{askQuestionText(t, ask)}</p>

      {kind === "permission" && ask.toolInput && (
        <div className="copilot-tool-detail agent-ask-tool-input">
          <span className="agent-ask-tool-input-label">{t("agents.ask.toolInput")}</span>
          <ToolInput input={ask.toolInput} t={t} />
        </div>
      )}
      {inputCut && isPending && (
        <p className="agent-ask-cut-warning" role="alert">
          {t("agents.ask.inputCut")}
        </p>
      )}

      {!isPending ? (
        <div className="agent-ask-result">
          <span className={`copilot-badge agent-ask-status-${ask.status}`}>
            {ask.status === "approved"
              ? kind === "permission"
                ? ask.decision === "approve_run" ||
                  ask.answer === "approve_run" ||
                  ask.note === "allowed for this run"
                  ? t("agents.ask.statusAllowedRun")
                  : t("agents.ask.statusAllowed")
                : t("agents.ask.statusApproved")
              : ask.status === "denied"
                ? kind === "question" || kind === "choice"
                  ? t("agents.ask.statusDismissed")
                  : t("agents.ask.statusDenied")
                : ask.status === "answered"
                  ? t("agents.ask.statusAnswered")
                  : t("agents.ask.expired")}
          </span>
          {ask.answer && kind !== "permission" && (
            <p className="agent-ask-result-answer">
              {t("agents.ask.resultAnswer", { answer: ask.answer })}
            </p>
          )}
          {ask.note && ask.note !== "allowed for this run" && (
            <p className="agent-ask-result-note">
              {t("agents.ask.resultNote", { note: ask.note })}
            </p>
          )}
        </div>
      ) : !canAnswer ? (
        <p className="agent-ask-not-yours">{t("agents.ask.notYours")}</p>
      ) : kind === "question" ? (
        <>
          <textarea
            className="agent-ask-textarea agent-ask-note"
            name="answer"
            aria-label={t("agents.ask.answerLabel")}
            placeholder={t("agents.ask.answerPlaceholder")}
            value={answerText}
            disabled={busy}
            onChange={(e) => setAnswerText(e.target.value)}
            rows={3}
          />
          <div className="copilot-permission-actions">
            <button
              type="button"
              className="primary"
              disabled={busy || !answerText.trim()}
              onClick={() =>
                onAnswer({
                  decision: "answer",
                  answer: answerText.trim(),
                  ...(note ? { note } : {}),
                })
              }
            >
              {t("agents.ask.send")}
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => onAnswer({ decision: "deny", ...(note ? { note } : {}) })}
            >
              {t("agents.ask.dismiss")}
            </button>
          </div>
        </>
      ) : kind === "choice" ? (
        <>
          <div className="copilot-permission-actions agent-ask-choice-actions">
            {ask.options?.map((opt) => (
              <button
                key={opt}
                type="button"
                className="primary"
                disabled={busy}
                onClick={() =>
                  onAnswer({
                    decision: "answer",
                    answer: opt,
                    ...(note ? { note } : {}),
                  })
                }
              >
                {opt}
              </button>
            ))}
            <button
              type="button"
              disabled={busy}
              onClick={() => onAnswer({ decision: "deny", ...(note ? { note } : {}) })}
            >
              {t("agents.ask.dismiss")}
            </button>
          </div>
          <input
            type="text"
            className="agent-ask-note"
            name="note"
            aria-label={t("agents.ask.noteLabel")}
            placeholder={t("agents.ask.notePlaceholder")}
            value={note}
            disabled={busy}
            onChange={(e) => setNote(e.target.value)}
          />
        </>
      ) : kind === "permission" ? (
        <div className="copilot-permission-actions">
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() => onAnswer({ decision: "approve" })}
          >
            {t("agents.ask.allow")} {!inputCut && <kbd>1</kbd>}
          </button>
          {canAllowRun && (
            <button
              type="button"
              disabled={busy}
              onClick={() => onAnswer({ decision: "approve_run" })}
            >
              {t("agents.ask.allowRun")} <kbd>2</kbd>
            </button>
          )}
          <button
            type="button"
            className="danger"
            disabled={busy}
            onClick={() => onAnswer({ decision: "deny" })}
          >
            {t("agents.ask.deny")} <kbd>3</kbd>
          </button>
        </div>
      ) : (
        <>
          <input
            type="text"
            className="agent-ask-note"
            name="note"
            aria-label={t("agents.ask.noteLabel")}
            placeholder={t("agents.ask.notePlaceholder")}
            value={note}
            disabled={busy}
            onChange={(e) => setNote(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                onAnswer({ decision: "approve", ...(note ? { note } : {}) });
              }
            }}
          />
          <div className="copilot-permission-actions">
            <button
              type="button"
              className="primary"
              disabled={busy}
              onClick={() => onAnswer({ decision: "approve", ...(note ? { note } : {}) })}
            >
              {t("agents.ask.approve")} <kbd>1</kbd>
            </button>
            <button
              type="button"
              className="danger"
              disabled={busy}
              onClick={() => onAnswer({ decision: "deny", ...(note ? { note } : {}) })}
            >
              {t("agents.ask.deny")} <kbd>2</kbd>
            </button>
          </div>
        </>
      )}
    </div>
  );
}
