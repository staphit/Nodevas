import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { IconClose } from "../icons";
import { useApp } from "../store";
import { AskCard } from "./AskCard";
import { useAgents } from "./agentStore";

function toDate(value: number): Date {
  return new Date(value < 1e12 ? value * 1000 : value);
}

export function AgentPanel({ onClose }: { onClose: () => void }) {
  const { language, t } = useI18n();
  const headingId = useId();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const sessions = useAgents((s) => s.sessions);
  const messages = useAgents((s) => s.messages);
  const asks = useAgents((s) => s.asks);
  const submittingAskIds = useAgents((s) => s.submittingAskIds);
  const showOffline = useAgents((s) => s.showOffline);
  const setShowOffline = useAgents((s) => s.setShowOffline);
  const selectedRecipient = useAgents((s) => s.selectedRecipient);
  const setSelectedRecipient = useAgents((s) => s.setSelectedRecipient);
  const replyToId = useAgents((s) => s.replyToId);
  const setReplyToId = useAgents((s) => s.setReplyToId);
  const filterNodeId = useAgents((s) => s.filterNodeId);
  const setFilterNodeId = useAgents((s) => s.setFilterNodeId);
  const sending = useAgents((s) => s.sending);
  const error = useAgents((s) => s.error);
  const setError = useAgents((s) => s.setError);
  const sendMessage = useAgents((s) => s.sendMessage);
  const answerAsk = useAgents((s) => s.answerAsk);
  const activeTab = useApp((s) => s.activeTab);
  const openTab = useApp((s) => s.openTab);

  const [draft, setDraft] = useState("");
  const [attachActiveNode, setAttachActiveNode] = useState(false);

  const pendingAsks = asks.filter((a) => a.status === "pending");

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape" && !event.defaultPrevented) {
      event.preventDefault();
      onClose();
    }
  };

  const handleSend = async () => {
    const ok = await sendMessage({
      to: selectedRecipient || "*",
      message: draft,
      replyTo: replyToId || undefined,
      nodeId: attachActiveNode && activeTab ? activeTab : filterNodeId || undefined,
    });
    if (ok) {
      setDraft("");
    }
  };

  return (
    <aside
      id="agent-network-panel"
      className="copilot-panel agent-panel"
      role="dialog"
      aria-modal="false"
      aria-labelledby={headingId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
    >
      <header className="copilot-header">
        <div className="copilot-header-row">
          <h2 id={headingId} className="copilot-heading">
            {t("agents.title")}
          </h2>
          <span className="copilot-model">
            {t("agents.onlineCount", {
              count: sessions.filter((s) => s.online).length,
            })}
          </span>
          <label className="agent-toggle-offline">
            <input
              type="checkbox"
              checked={showOffline}
              onChange={(e) => setShowOffline(e.target.checked)}
            />
            <span>{t("agents.showOffline")}</span>
          </label>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={onClose}
            aria-label={t("agents.close")}
            title={`${t("agents.close")} (Esc)`}
          >
            <IconClose size={14} />
          </button>
        </div>
      </header>

      {error && (
        <div className="copilot-banner" role="status">
          <span>{error}</span>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={() => setError(null)}
            aria-label={t("agents.dismiss")}
          >
            <IconClose size={12} />
          </button>
        </div>
      )}

      {pendingAsks.length > 0 && (
        <section className="agent-asks-section" aria-label={t("agents.ask.section")}>
          {pendingAsks.map((ask) => (
            <AskCard
              key={ask.id}
              ask={ask}
              submitting={submittingAskIds[ask.id]}
              onSelectNode={(nodeId) => void openTab(nodeId)}
              onAnswer={(decision, note) => void answerAsk(ask.id, decision, note)}
            />
          ))}
        </section>
      )}

      <section className="agent-roster-section" aria-label={t("agents.roster")}>
        {sessions.length === 0 ? (
          <div className="agent-roster-empty">{t("agents.emptyRoster")}</div>
        ) : (
          <div className="agent-roster-list">
            {sessions.map((s) => {
              const isTarget = selectedRecipient === s.name;
              const repoBranch = s.repo
                ? s.branch
                  ? `${s.repo}@${s.branch}`
                  : s.repo
                : "";
              return (
                <div
                  key={s.id}
                  className={`agent-session-card${isTarget ? " is-selected" : ""}${
                    !s.online ? " is-offline" : ""
                  }`}
                >
                  <div className="agent-session-top">
                    <span
                      className={`agent-state-dot state-${
                        s.online ? s.state : "offline"
                      }`}
                      title={s.online ? s.state : "offline"}
                    />
                    <button
                      type="button"
                      className="agent-session-name"
                      onClick={() => setSelectedRecipient(s.name)}
                      title={t("agents.targetSession", { name: s.name })}
                    >
                      {s.actorName ? `${s.actorName}/${s.name}` : s.name}
                    </button>
                    <span className="copilot-badge">{s.agent}</span>
                    {s.role && <span className="copilot-badge">{s.role}</span>}
                  </div>
                  {(repoBranch || s.doing || s.nodeId) && (
                    <div className="agent-session-meta">
                      {repoBranch && (
                        <span className="agent-session-repo">{repoBranch}</span>
                      )}
                      {s.nodeId && (
                        <button
                          type="button"
                          className="agent-node-chip"
                          onClick={() => void openTab(s.nodeId!)}
                        >
                          #{s.nodeId}
                        </button>
                      )}
                      {s.doing && (
                        <span className="agent-session-doing">{s.doing}</span>
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      <div className="agent-filter-bar">
        <span className="agent-section-label">{t("agents.messages")}</span>
        {filterNodeId ? (
          <button
            type="button"
            className="agent-node-chip is-active"
            onClick={() => setFilterNodeId("")}
            title={t("agents.clearNodeFilter")}
          >
            #{filterNodeId} ×
          </button>
        ) : (
          activeTab && (
            <button
              type="button"
              className="agent-node-chip"
              onClick={() => setFilterNodeId(activeTab)}
            >
              {t("agents.filterByNode", { id: activeTab })}
            </button>
          )
        )}
      </div>

      <div className="copilot-transcript agent-messages-list">
        {messages.length === 0 ? (
          <p className="copilot-empty">{t("agents.emptyMessages")}</p>
        ) : (
          messages.map((m) => {
            const fromHuman = m.senderAgent === "human";
            return (
              <div
                key={m.id}
                className={`copilot-bubble ${
                  fromHuman ? "copilot-bubble-user" : "copilot-bubble-assistant"
                } agent-msg-bubble`}
              >
                <div className="agent-msg-head">
                  <span className="agent-msg-route">
                    <strong>{m.from}</strong> → <span>{m.to}</span>
                  </span>
                  {m.nodeId && (
                    <button
                      type="button"
                      className="agent-node-chip"
                      onClick={() => void openTab(m.nodeId!)}
                    >
                      #{m.nodeId}
                    </button>
                  )}
                  <button
                    type="button"
                    className="agent-reply-btn"
                    onClick={() => {
                      if (m.senderSession && m.senderSession !== "web") {
                        setSelectedRecipient(m.senderSession);
                      }
                      setReplyToId(m.id);
                      inputRef.current?.focus();
                    }}
                  >
                    {t("agents.reply")}
                  </button>
                </div>
                <div className="agent-msg-body">{m.body}</div>
                <div className="agent-msg-time">
                  {formatLocalizedDateTime(toDate(m.createdAt), language)}
                </div>
              </div>
            );
          })
        )}
      </div>

      <footer className="copilot-composer agent-composer">
        <div className="agent-composer-top">
          <label className="agent-to-label">
            <span>{t("agents.to")}</span>
            <select
              className="copilot-select"
              value={selectedRecipient}
              onChange={(e) => setSelectedRecipient(e.target.value)}
            >
              <option value="*">{t("agents.broadcastAll")}</option>
              {sessions.map((s) => (
                <option key={s.id} value={s.name}>
                  {s.actorName ? `${s.actorName}/${s.name}` : s.name} ({s.agent})
                </option>
              ))}
              {activeTab && (
                <option value={`node:${activeTab}`}>node:{activeTab}</option>
              )}
            </select>
          </label>
          {replyToId && (
            <button
              type="button"
              className="agent-node-chip is-active"
              onClick={() => setReplyToId("")}
            >
              reply:{replyToId.slice(0, 6)} ×
            </button>
          )}
          {activeTab && (
            <label className="agent-toggle-offline">
              <input
                type="checkbox"
                checked={attachActiveNode}
                onChange={(e) => setAttachActiveNode(e.target.checked)}
              />
              <span>#{activeTab}</span>
            </label>
          )}
        </div>
        <div className="agent-composer-row">
          <textarea
            ref={inputRef}
            className="copilot-input"
            rows={2}
            placeholder={t("agents.placeholder")}
            value={draft}
            disabled={sending}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void handleSend();
              }
            }}
          />
          <button
            type="button"
            className="copilot-send primary"
            disabled={sending || !draft.trim()}
            onClick={() => void handleSend()}
            aria-label={t("agents.send")}
            title={t("agents.send")}
          >
            ↑
          </button>
        </div>
      </footer>
    </aside>
  );
}
