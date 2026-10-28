import {
  useEffect,
  useId,
  useRef,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
} from "react";
import { api, type AgentFileUpload, type AgentMessage } from "../api";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { IconBell, IconClose, IconComputer, IconPaperclip, IconUsage } from "../icons";
import { WaitingForYouButton } from "../autopilot/HumanReview";
import { useApp } from "../store";
import { AgentChatEmpty } from "./AgentChatEmpty";
import { AskCard } from "./AskCard";
import { canManageSession, useAgents } from "./agentStore";
import {
  addAttachments,
  formatFileSize,
  MAX_AGENT_FILE_BYTES,
  MAX_AGENT_FILES,
  type AttachmentError,
} from "./attachments";
import {
  requestSystemNotifications,
  systemNotifyPermission,
  type SystemNotifyPermission,
} from "./systemNotify";

function toDate(value: number): Date {
  return new Date(value < 1e12 ? value * 1000 : value);
}

const NOTIFY_PROMPT_KEY = "nodevas-agent-notify-prompt";

function promptDismissed(): boolean {
  try {
    return localStorage.getItem(NOTIFY_PROMPT_KEY) === "dismissed";
  } catch {
    return false;
  }
}

/** A message's files: download links once delivered, plain names while held. */
function FileChips({ message }: { message: AgentMessage }) {
  const { t } = useI18n();
  const files = message.files ?? [];
  if (files.length === 0) return null;
  // The hub refuses the recipient side a held message's files, so a held
  // message only names them.
  const downloadable = message.status !== "held";
  return (
    <div className="agent-file-chips" aria-label={t("agents.files.label")}>
      {files.map((file) =>
        downloadable ? (
          <a
            key={file.name}
            className="agent-file-chip"
            href={api.agentMessageFileURL(message.id, file.name)}
            download={file.name}
            title={t("agents.files.download", { name: file.name })}
          >
            <IconPaperclip size={11} />
            <span className="agent-file-name">{file.name}</span>
            <span className="agent-file-size">{formatFileSize(file.size)}</span>
          </a>
        ) : (
          <span key={file.name} className="agent-file-chip is-static">
            <IconPaperclip size={11} />
            <span className="agent-file-name">{file.name}</span>
            <span className="agent-file-size">{formatFileSize(file.size)}</span>
          </span>
        ),
      )}
    </div>
  );
}

/**
 * The agent network: who is online, what waits on you (asks, held mail) and
 * the messages. Floating over the board in Info; docked in Station's right
 * column (docked), where it stays put: no close button, no focus grab.
 */
export function AgentPanel({ onClose, docked = false }: { onClose?: () => void; docked?: boolean }) {
  const { language, t } = useI18n();
  const headingId = useId();
  const heldHeadingId = useId();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const sessions = useAgents((s) => s.sessions);
  const messages = useAgents((s) => s.messages);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const submittingAskIds = useAgents((s) => s.submittingAskIds);
  const decidingIds = useAgents((s) => s.decidingIds);
  const inboundPendingIds = useAgents((s) => s.inboundPendingIds);
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
  const decideHeld = useAgents((s) => s.decideHeld);
  const setInbound = useAgents((s) => s.setInbound);
  const activeTab = useApp((s) => s.activeTab);
  const openTab = useApp((s) => s.openTab);

  const [draft, setDraft] = useState("");
  const [attachActiveNode, setAttachActiveNode] = useState(false);
  const [notify, setNotify] = useState(false);
  const [attachments, setAttachments] = useState<AgentFileUpload[]>([]);
  const [attachErrors, setAttachErrors] = useState<AttachmentError[]>([]);
  const [permission, setPermission] = useState<SystemNotifyPermission>(() =>
    systemNotifyPermission(),
  );
  const [promptHidden, setPromptHidden] = useState(promptDismissed);

  const pendingAsks = asks.filter((a) => a.status === "pending");

  useEffect(() => {
    if (!docked) inputRef.current?.focus();
  }, [docked]);

  // Open, the panel is being read: what is in it is no longer unread.
  useEffect(() => {
    useAgents.getState().markSeen();
  }, [messages]);

  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape" && !event.defaultPrevented && onClose) {
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
      notify,
      files: attachments,
    });
    if (ok) {
      setDraft("");
      setAttachments([]);
      setAttachErrors([]);
      setNotify(false);
    }
  };

  const onPickFiles = async (event: ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(event.target.files ?? []);
    // Clear the input so picking the same file again still fires a change.
    event.target.value = "";
    if (picked.length === 0) return;
    const result = await addAttachments(attachments, picked);
    setAttachments(result.files);
    setAttachErrors(result.errors);
  };

  const enableNotifications = async () => {
    setPermission(await requestSystemNotifications());
  };

  const hideNotifyPrompt = () => {
    setPromptHidden(true);
    try {
      localStorage.setItem(NOTIFY_PROMPT_KEY, "dismissed");
    } catch {
      /* the prompt just comes back next time */
    }
  };

  const attachErrorText = (err: AttachmentError) =>
    t(`agents.files.error.${err.code}`, {
      name: err.name,
      max: MAX_AGENT_FILES,
      kb: MAX_AGENT_FILE_BYTES / 1024,
    });

  return (
    <aside
      id={docked ? "agent-network-docked" : "agent-network-panel"}
      className={`copilot-panel agent-panel${docked ? " is-docked" : ""}`}
      role={docked ? "region" : "dialog"}
      aria-modal={docked ? undefined : "false"}
      aria-labelledby={headingId}
      tabIndex={-1}
      onKeyDown={onKeyDown}
    >
      <header className="copilot-header">
        <div className="copilot-header-row">
          {/* Docked, the Station card's toggle right above already reads
              "Agent chat · N online"; the heading stays only to name the
              region for screen readers. */}
          <h2 id={headingId} className={docked ? "copilot-heading sr-only" : "copilot-heading"}>
            {t("agents.title")}
          </h2>
          {!docked && (
            <span className="copilot-model">
              {t("agents.onlineCount", {
                count: sessions.filter((s) => s.online).length,
              })}
            </span>
          )}
          <label className="agent-toggle-offline">
            <input
              type="checkbox"
              checked={showOffline}
              onChange={(e) => setShowOffline(e.target.checked)}
            />
            <span>{t("agents.showOffline")}</span>
          </label>
          <WaitingForYouButton />
          {/* Runners live here rather than in the top bar: they are where
              agents run, and the top bar has no room left at narrow widths. */}
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={() => window.dispatchEvent(new CustomEvent("nodevas-open-runners"))}
            aria-label={t("topbar.runners")}
            title={t("topbar.runnersTitle")}
          >
            <IconComputer size={14} />
          </button>
          <button
            type="button"
            className="copilot-icon-btn"
            onClick={() => window.dispatchEvent(new CustomEvent("nodevas-open-usage"))}
            aria-label={t("topbar.usage")}
            title={t("topbar.usageTitle")}
          >
            <IconUsage size={14} />
          </button>
          {onClose && (
            <button
              type="button"
              className="copilot-icon-btn"
              onClick={onClose}
              aria-label={t("agents.close")}
              title={`${t("agents.close")} (Esc)`}
            >
              <IconClose size={14} />
            </button>
          )}
        </div>
      </header>

      {permission === "default" && !promptHidden && (
        <div className="agent-notify-prompt">
          <IconBell size={13} />
          <button
            type="button"
            className="agent-notify-enable"
            onClick={() => void enableNotifications()}
          >
            {t("agents.notify.enableDesktop")}
          </button>
          <button
            type="button"
            className="copilot-icon-btn agent-notify-prompt-close"
            onClick={hideNotifyPrompt}
            aria-label={t("agents.dismiss")}
          >
            <IconClose size={11} />
          </button>
        </div>
      )}

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
              canAnswer={canManageSession(ask, viewer)}
              onSelectNode={(nodeId) => void openTab(nodeId)}
              onAnswer={(decision, note) => void answerAsk(ask.id, decision, note)}
            />
          ))}
        </section>
      )}

      {held.length > 0 && (
        <section className="agent-held-section" aria-labelledby={heldHeadingId}>
          <h3 id={heldHeadingId} className="agent-section-label agent-held-heading">
            {t("agents.held.section", { count: held.length })}
          </h3>
          {held.map((m) => {
            const busy = Boolean(decidingIds[m.id]);
            return (
              <div key={m.id} className="agent-held-card" data-message-id={m.id}>
                <div className="agent-held-route">
                  <strong>{m.from}</strong>
                  <span aria-hidden> → </span>
                  <span className="sr-only">{t("agents.held.to")}</span>
                  <span>{m.to}</span>
                </div>
                <div className="agent-held-body">{m.body}</div>
                <FileChips message={m} />
                <div className="copilot-permission-actions agent-held-actions">
                  <button
                    type="button"
                    className="primary"
                    disabled={busy}
                    onClick={() => void decideHeld(m.id, "approve")}
                  >
                    {t("agents.held.approve")}
                  </button>
                  <button
                    type="button"
                    className="danger"
                    disabled={busy}
                    onClick={() => void decideHeld(m.id, "reject")}
                  >
                    {t("agents.held.reject")}
                  </button>
                </div>
              </div>
            );
          })}
        </section>
      )}

      <section
        className={`agent-roster-section${sessions.length === 0 ? " is-intro" : ""}`}
        aria-label={t("agents.roster")}
      >
        {sessions.length === 0 ? (
          <AgentChatEmpty />
        ) : (
          <div className="agent-roster-list">
            {sessions.map((s) => {
              const isTarget = selectedRecipient === s.name;
              const holding = s.inbound === "hold";
              const repoBranch = s.repo
                ? s.branch
                  ? `${s.repo}@${s.branch}`
                  : s.repo
                : "";
              const label = s.actorName ? `${s.actorName}/${s.name}` : s.name;
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
                      {label}
                    </button>
                    <span className="copilot-badge">{s.agent}</span>
                    {s.model && (
                      <span className="copilot-badge agent-model-badge" title={s.model}>
                        {s.model}
                      </span>
                    )}
                    {s.role && <span className="copilot-badge">{s.role}</span>}
                    {canManageSession(s, viewer) ? (
                      <button
                        type="button"
                        role="switch"
                        aria-checked={holding}
                        className={`agent-inbound-toggle${holding ? " is-hold" : ""}`}
                        disabled={Boolean(inboundPendingIds[s.id])}
                        onClick={() => void setInbound(s.id, holding ? "open" : "hold")}
                        aria-label={t("agents.inbound.toggleLabel", { name: label })}
                        title={t(holding ? "agents.inbound.holdTitle" : "agents.inbound.openTitle")}
                      >
                        {t(holding ? "agents.inbound.hold" : "agents.inbound.open")}
                      </button>
                    ) : (
                      holding && (
                        <span
                          className="copilot-badge agent-inbound-badge"
                          title={t("agents.inbound.holdTitle")}
                        >
                          {t("agents.inbound.hold")}
                        </span>
                      )
                    )}
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
            const status = m.status === "held" || m.status === "rejected" ? m.status : "";
            return (
              <div
                key={m.id}
                className={`copilot-bubble ${
                  fromHuman ? "copilot-bubble-user" : "copilot-bubble-assistant"
                } agent-msg-bubble${status ? ` is-${status}` : ""}`}
              >
                <div className="agent-msg-head">
                  <span className="agent-msg-route">
                    <strong>{m.from}</strong> → <span>{m.to}</span>
                  </span>
                  {m.notify && (
                    <span className="agent-msg-notify" title={t("agents.notify.sent")}>
                      <IconBell size={11} />
                      <span className="sr-only">{t("agents.notify.sent")}</span>
                    </span>
                  )}
                  {status && (
                    <span className={`copilot-badge agent-status-badge status-${status}`}>
                      {t(`agents.status.${status}`)}
                    </span>
                  )}
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
                <FileChips message={m} />
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
          <span className="agent-composer-tools">
            <button
              type="button"
              className={`copilot-icon-btn agent-composer-tool${notify ? " is-on" : ""}`}
              aria-pressed={notify}
              onClick={() => setNotify((v) => !v)}
              aria-label={t("agents.notify.toggle")}
              title={t(notify ? "agents.notify.onTitle" : "agents.notify.offTitle")}
            >
              <IconBell size={14} />
            </button>
            <button
              type="button"
              className="copilot-icon-btn agent-composer-tool"
              onClick={() => fileInputRef.current?.click()}
              disabled={sending || attachments.length >= MAX_AGENT_FILES}
              aria-label={t("agents.files.attach")}
              title={t("agents.files.attachTitle", {
                max: MAX_AGENT_FILES,
                kb: MAX_AGENT_FILE_BYTES / 1024,
              })}
            >
              <IconPaperclip size={14} />
            </button>
          </span>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            data-testid="agent-file-input"
            onChange={(e) => void onPickFiles(e)}
          />
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
        {attachments.length > 0 && (
          <div className="agent-file-chips agent-attachments" aria-label={t("agents.files.attached")}>
            {attachments.map((file) => (
              <span key={file.name} className="agent-file-chip is-static">
                <IconPaperclip size={11} />
                <span className="agent-file-name">{file.name}</span>
                <button
                  type="button"
                  className="agent-file-remove"
                  onClick={() =>
                    setAttachments((list) => list.filter((f) => f.name !== file.name))
                  }
                  aria-label={t("agents.files.remove", { name: file.name })}
                >
                  ×
                </button>
              </span>
            ))}
          </div>
        )}
        {attachErrors.length > 0 && (
          <ul className="agent-attach-errors" role="alert">
            {attachErrors.map((err, index) => (
              <li key={`${index}:${err.code}:${err.name}`}>{attachErrorText(err)}</li>
            ))}
          </ul>
        )}
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
