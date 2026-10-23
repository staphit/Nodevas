import { useId, useState, type KeyboardEvent } from "react";
import type { AgentAsk } from "../api";
import { useI18n } from "../i18n";

export function AskCard({
  ask,
  submitting,
  onSelectNode,
  onAnswer,
}: {
  ask: AgentAsk;
  submitting?: boolean;
  onSelectNode?: (nodeId: string) => void;
  onAnswer: (decision: "approved" | "denied", note: string) => void;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const [note, setNote] = useState("");
  const busy = Boolean(submitting);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey || event.altKey || busy) return;
    if ((event.target as HTMLElement)?.tagName === "INPUT") return;
    if (event.key === "1") {
      event.preventDefault();
      onAnswer("approved", note);
    } else if (event.key === "2") {
      event.preventDefault();
      onAnswer("denied", note);
    }
  };

  return (
    <div
      className="copilot-permission agent-ask-card"
      role="group"
      aria-labelledby={titleId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      data-ask-id={ask.id}
    >
      <div className="copilot-permission-head">
        <span id={titleId} className="copilot-permission-title">
          {t("agents.ask.from", { session: ask.sessionName || ask.sessionId })}
        </span>
        {ask.agent && <span className="copilot-badge">{ask.agent}</span>}
        {ask.nodeId && (
          <button
            type="button"
            className="agent-node-chip"
            onClick={() => onSelectNode?.(ask.nodeId!)}
          >
            #{ask.nodeId}
          </button>
        )}
      </div>
      <p className="copilot-permission-summary">{ask.question}</p>
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
            onAnswer("approved", note);
          }
        }}
      />
      <div className="copilot-permission-actions">
        <button
          type="button"
          className="primary"
          disabled={busy}
          onClick={() => onAnswer("approved", note)}
        >
          {t("agents.ask.approve")} <kbd>1</kbd>
        </button>
        <button
          type="button"
          className="danger"
          disabled={busy}
          onClick={() => onAnswer("denied", note)}
        >
          {t("agents.ask.deny")} <kbd>2</kbd>
        </button>
      </div>
    </div>
  );
}
