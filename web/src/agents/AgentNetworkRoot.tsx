import { lazy, Suspense, useEffect, useRef } from "react";
import { useViewer } from "../components/SignIn";
import { useI18n } from "../i18n";
import { IconClose } from "../icons";
import { useApp } from "../store";
import { useAgents } from "./agentStore";

const AgentPanel = lazy(() =>
  import("./AgentPanel").then((m) => ({ default: m.AgentPanel })),
);

/** notify=true messages, stacked above the floating button. */
function AgentToasts() {
  const { t } = useI18n();
  const toasts = useAgents((s) => s.toasts);
  const dismissToast = useAgents((s) => s.dismissToast);
  const setOpen = useAgents((s) => s.setOpen);
  if (toasts.length === 0) return null;
  return (
    <div className="agent-toasts" role="status" aria-live="polite">
      {toasts.map((toast) => (
        <div key={toast.id} className="agent-toast" data-message-id={toast.messageId}>
          <button
            type="button"
            className="agent-toast-body"
            onClick={() => {
              dismissToast(toast.id);
              setOpen(true);
            }}
            title={t("agents.notify.openPanel")}
          >
            <strong>{toast.from}</strong>: {toast.body}
          </button>
          <button
            type="button"
            className="agent-toast-close"
            onClick={() => dismissToast(toast.id)}
            aria-label={t("agents.dismiss")}
          >
            <IconClose size={12} />
          </button>
        </div>
      ))}
    </div>
  );
}

export function AgentNetworkRoot() {
  const { t } = useI18n();
  const viewer = useViewer();
  const open = useAgents((s) => s.open);
  const setOpen = useAgents((s) => s.setOpen);
  const sessions = useAgents((s) => s.sessions);
  const asks = useAgents((s) => s.asks);
  const heldCount = useAgents((s) => s.held.length);
  const refreshAll = useAgents((s) => s.refreshAll);
  const setViewer = useAgents((s) => s.setViewer);
  const activeProject = useApp((s) => s.activeProject);

  const onlineCount = sessions.filter((s) => s.online).length;
  const pendingAskCount = asks.filter((a) => a.status === "pending").length;
  const waitingCount = pendingAskCount + heldCount;
  const prevPendingRef = useRef(0);

  useEffect(() => {
    setViewer(viewer);
  }, [viewer, setViewer]);

  useEffect(() => {
    void refreshAll();
  }, [activeProject, refreshAll]);

  // Auto-open the panel when a new ask_user request arrives from an agent.
  useEffect(() => {
    if (pendingAskCount > prevPendingRef.current) {
      setOpen(true);
    }
    prevPendingRef.current = pendingAskCount;
  }, [pendingAskCount, setOpen]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        (event.ctrlKey || event.metaKey) &&
        event.shiftKey &&
        !event.altKey &&
        (event.key === "A" || event.key === "a")
      ) {
        event.preventDefault();
        if (!event.repeat) useAgents.getState().toggleOpen();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <>
      {!open && (
        <button
          type="button"
          className={`agent-fab${waitingCount > 0 ? " has-pending-ask" : ""}${
            onlineCount > 0 ? " has-online" : ""
          }`}
          onClick={() => setOpen(true)}
          aria-label={t("agents.open")}
          aria-expanded={false}
          aria-controls="agent-network-panel"
          title={`${t("agents.open")} (Ctrl+Shift+A)`}
        >
          <span className="agent-fab-icon" aria-hidden>
            ⇄
          </span>
          {(waitingCount > 0 || onlineCount > 0) && (
            <span className="agent-fab-badge">
              {waitingCount > 0 ? `!${waitingCount}` : onlineCount}
            </span>
          )}
        </button>
      )}
      {!open && <AgentToasts />}
      {open && (
        <Suspense fallback={null}>
          <AgentPanel onClose={() => setOpen(false)} />
        </Suspense>
      )}
    </>
  );
}
