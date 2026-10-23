import { lazy, Suspense, useEffect, useRef } from "react";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { useAgents } from "./agentStore";

const AgentPanel = lazy(() =>
  import("./AgentPanel").then((m) => ({ default: m.AgentPanel })),
);

export function AgentNetworkRoot() {
  const { t } = useI18n();
  const open = useAgents((s) => s.open);
  const setOpen = useAgents((s) => s.setOpen);
  const sessions = useAgents((s) => s.sessions);
  const asks = useAgents((s) => s.asks);
  const refreshAll = useAgents((s) => s.refreshAll);
  const activeProject = useApp((s) => s.activeProject);

  const onlineCount = sessions.filter((s) => s.online).length;
  const pendingAskCount = asks.filter((a) => a.status === "pending").length;
  const prevPendingRef = useRef(0);

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
          className={`agent-fab${pendingAskCount > 0 ? " has-pending-ask" : ""}${
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
          {(pendingAskCount > 0 || onlineCount > 0) && (
            <span className="agent-fab-badge">
              {pendingAskCount > 0 ? `!${pendingAskCount}` : onlineCount}
            </span>
          )}
        </button>
      )}
      {open && (
        <Suspense fallback={null}>
          <AgentPanel onClose={() => setOpen(false)} />
        </Suspense>
      )}
    </>
  );
}
