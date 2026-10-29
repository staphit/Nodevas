import { useEffect, useId, useState, type KeyboardEvent } from "react";
import type { AgentAsk, AgentAskAnswerRequest } from "../api";
import { useI18n } from "../i18n";

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
        <span id={titleId} className="copilot-permission-title">
          {getTitle()}
        </span>
        {ask.agent && <span className="copilot-badge">{ask.agent}</span>}
        {kind === "permission" && ask.tool && <span className="copilot-badge">{ask.tool}</span>}
        {ask.runId && (
          <span className="agent-node-chip">
            {t("agents.ask.run", { runId: ask.runId })}
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

      <p className="copilot-permission-summary">{ask.question}</p>

      {kind === "permission" && ask.toolInput && (
        <details className="copilot-tool-detail">
          <summary>{t("agents.ask.toolInput")}</summary>
          <pre>{ask.toolInput}</pre>
        </details>
      )}

      {!isPending ? (
        <div className="agent-ask-result">
          <span className={`copilot-badge agent-ask-status-${ask.status}`}>
            {ask.status === "approved"
              ? t("agents.ask.statusApproved")
              : ask.status === "denied"
                ? t("agents.ask.statusDenied")
                : ask.status === "answered"
                  ? t("agents.ask.statusAnswered")
                  : t("agents.ask.expired")}
          </span>
          {ask.answer && (
            <p className="agent-ask-result-answer">
              {t("agents.ask.resultAnswer", { answer: ask.answer })}
            </p>
          )}
          {ask.note && (
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
            {t("agents.ask.allow")} <kbd>1</kbd>
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => onAnswer({ decision: "approve_run" })}
          >
            {t("agents.ask.allowRun")} <kbd>2</kbd>
          </button>
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
