// What a click in the war room does. Every ClickTarget the world model hands
// out maps to a real action of the app: open the node, open the run in the
// drawer, show a diff, answer an ask, decide a held message, follow a PR
// link, go to another project's office (and do something there once it has
// loaded), open an autopilot or the runners. The views never invent
// behaviour of their own.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

import type { SessionChange } from "../api/runs";
import type { AgentAskKind } from "../api";
import { AskCard } from "../agents/AskCard";
import { canAnswerAsk, useAgents } from "../agents/agentStore";
import { openAutopilot } from "../autopilot/openAutopilot";
import { useI18n } from "../i18n";
import { RunDiffDialog } from "../runs/DiffViewer";
import { SessionDiffDialog } from "../runs/NodeSessions";
import { openNodeRuns } from "../runs/openRuns";
import { useRuns } from "../runs/runStore";
import { useSessions } from "../runs/sessionStore";
import { openSettings } from "../settings/openSettings";
import { useStation } from "../station/stationStore";
import { reportError, useApp } from "../store";
import { mergeRuns } from "./useWorld";
import type { ClickTarget } from "./world";

type Dialog =
  | { kind: "run-diff"; runId: string }
  | { kind: "session-diff"; change: SessionChange }
  | { kind: "ask"; askId: string }
  | { kind: "held"; messageId: string }
  | { kind: "notice"; text: string };

export interface GameActions {
  dispatch: (target: ClickTarget) => void;
}

const GameActionsContext = createContext<GameActions>({ dispatch: () => undefined });

export function useGameActions(): GameActions {
  return useContext(GameActionsContext);
}

function findRun(runId: string) {
  const state = useRuns.getState();
  return mergeRuns(state.activeRuns, state.latestByNode, state.runsByNode)
    .reverse()
    .find((run) => run.id === runId);
}

/** Opens a link the hub reported, only over http(s), never with an opener. */
function openExternal(url: string) {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") return;
    window.open(parsed.href, "_blank", "noopener,noreferrer");
  } catch {
    /* not a URL: nothing to open */
  }
}

/** Provides dispatch and draws the dialogs it opens. */
export function GameActionsProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const [dialog, setDialog] = useState<Dialog | null>(null);
  // Focus goes back where it was when a dialog closes.
  const returnFocus = useRef<HTMLElement | null>(null);

  const open = useCallback((next: Dialog) => {
    returnFocus.current = document.activeElement as HTMLElement | null;
    setDialog(next);
  }, []);

  const close = useCallback(() => {
    setDialog(null);
    const back = returnFocus.current;
    returnFocus.current = null;
    if (back?.isConnected) setTimeout(() => back.focus(), 0);
  }, []);

  // An in-project target already in its project dispatches what it wraps.
  const dispatchRef = useRef<(target: ClickTarget) => void>(() => undefined);
  const dispatch = useCallback(
    (target: ClickTarget) => {
      switch (target.kind) {
        case "node":
          void useApp.getState().openTab(target.nodeId).catch(reportError);
          return;
        case "run":
          openNodeRuns(target.nodeId, target.runId);
          return;
        case "run-diff":
          open({ kind: "run-diff", runId: target.runId });
          return;
        case "session-diff": {
          // The session's snapshot is the diff it left at Stop; its node's
          // session list holds it (SessionChange.sessionId = session id).
          void useSessions
            .getState()
            .load(target.nodeId)
            .then(() => {
              const change = (useSessions.getState().byNode[target.nodeId] ?? []).find(
                (item) => item.sessionId === target.sessionId,
              );
              if (change?.snapshot) open({ kind: "session-diff", change });
              else open({ kind: "notice", text: t("game.dialog.noSessionDiff") });
            });
          return;
        }
        case "session":
          useAgents.getState().setOpen(true);
          return;
        case "ask":
          open({ kind: "ask", askId: target.askId });
          return;
        case "held-message":
          open({ kind: "held", messageId: target.messageId });
          return;
        case "pr":
          openExternal(target.url);
          return;
        case "project":
          void useApp.getState().switchProject(target.project).catch(reportError);
          return;
        case "in-project": {
          // Done by StationView once the project's own stores are loaded.
          if (useApp.getState().activeProject === target.project) {
            dispatchRef.current(target.then);
            return;
          }
          useStation.getState().deferTarget(target.project, target.then);
          void useApp
            .getState()
            .switchProject(target.project)
            .then(() => useStation.getState().deferredReady(target.project))
            .catch((error) => {
              useStation.getState().clearDeferred();
              reportError(error);
            });
          return;
        }
        case "autopilot":
          openAutopilot(target.goalNodeId);
          return;
        case "runners":
          openSettings("runners");
          return;
      }
    },
    [open, t],
  );

  dispatchRef.current = dispatch;
  const value = useMemo(() => ({ dispatch }), [dispatch]);

  return (
    <GameActionsContext.Provider value={value}>
      {children}
      {dialog && <GameDialogView dialog={dialog} onClose={close} />}
    </GameActionsContext.Provider>
  );
}

function GameDialogView({ dialog, onClose }: { dialog: Dialog; onClose: () => void }) {
  const { t } = useI18n();
  if (dialog.kind === "run-diff") {
    const run = findRun(dialog.runId);
    if (!run) {
      return (
        <GameDialog title={t("game.action.runDiff")} onClose={onClose}>
          <p>{t("game.dialog.runGone")}</p>
        </GameDialog>
      );
    }
    return <RunDiffDialog run={run} onClose={onClose} />;
  }
  if (dialog.kind === "session-diff") {
    return <SessionDiffDialog change={dialog.change} onClose={onClose} />;
  }
  if (dialog.kind === "ask") return <AskDialog askId={dialog.askId} onClose={onClose} />;
  if (dialog.kind === "held") return <HeldDialog messageId={dialog.messageId} onClose={onClose} />;
  return (
    <GameDialog title={t("game.dialog.notice")} onClose={onClose}>
      <p>{dialog.text}</p>
    </GameDialog>
  );
}

export function askDialogTitle(
  t: (key: string, values?: Record<string, string | number>) => string,
  kind?: AgentAskKind,
): string {
  switch (kind) {
    case "approval":
      return t("game.action.askApproval");
    case "choice":
      return t("game.action.askChoice");
    case "permission":
      return t("game.action.askPermission");
    case "question":
    default:
      return t("game.action.ask");
  }
}

// A question's answer box, else the card itself, whose number keys answer;
// the close button first would swallow them.
const ASK_FOCUS = ["[data-ask-id] textarea:not(:disabled)", "[data-ask-id]"] as const;

function AskDialog({ askId, onClose }: { askId: string; onClose: () => void }) {
  const { t } = useI18n();
  const ask = useAgents((s) => s.asks.find((item) => item.id === askId));
  const submitting = useAgents((s) => Boolean(s.submittingAskIds[askId]));
  const answerAsk = useAgents((s) => s.answerAsk);
  const viewer = useAgents((s) => s.viewer);
  // Why the last answer failed: shown here, since the drawer that shows the
  // store's error is closed behind the dialog.
  const [failure, setFailure] = useState<string | null>(null);
  return (
    <GameDialog title={askDialogTitle(t, ask?.kind)} onClose={onClose} initialFocus={ASK_FOCUS}>
      {failure && (
        <p className="game-dialog-error" role="alert">
          {failure}
        </p>
      )}
      {ask && ask.status === "pending" ? (
        <AskCard
          ask={ask}
          submitting={submitting}
          canAnswer={canAnswerAsk(ask, viewer)}
          onSelectNode={(nodeId) => {
            onClose();
            void useApp.getState().openTab(nodeId).catch(reportError);
          }}
          onAnswer={(payload) => {
            setFailure(null);
            void answerAsk(ask.id, payload).then((ok) => {
              if (ok) onClose();
              else setFailure(useAgents.getState().error || t("game.dialog.answerFailed"));
            });
          }}
        />
      ) : (
        <p>{t("game.dialog.askGone")}</p>
      )}
    </GameDialog>
  );
}

function HeldDialog({ messageId, onClose }: { messageId: string; onClose: () => void }) {
  const { t } = useI18n();
  const message = useAgents((s) => s.held.find((item) => item.id === messageId));
  const deciding = useAgents((s) => Boolean(s.decidingIds[messageId]));
  const decideHeld = useAgents((s) => s.decideHeld);
  return (
    <GameDialog title={t("game.action.held")} onClose={onClose}>
      {message ? (
        <div className="game-held">
          <p className="game-held-route">
            {t("game.dialog.heldRoute", { from: message.from, to: message.to })}
          </p>
          <blockquote className="game-held-body">{message.body}</blockquote>
          <div className="game-dialog-actions">
            <button
              type="button"
              className="primary"
              disabled={deciding}
              onClick={() => void decideHeld(message.id, "approve").then(onClose)}
            >
              {t("game.dialog.approve")}
            </button>
            <button
              type="button"
              disabled={deciding}
              onClick={() => void decideHeld(message.id, "reject").then(onClose)}
            >
              {t("game.dialog.reject")}
            </button>
          </div>
        </div>
      ) : (
        <p>{t("game.dialog.heldGone")}</p>
      )}
    </GameDialog>
  );
}

/** A small modal: backdrop, title, close button, Escape, initial focus. */
export function GameDialog({
  title,
  onClose,
  initialFocus,
  children,
}: {
  title: string;
  onClose: () => void;
  /** Selectors tried in order for what takes focus when the dialog opens,
   * before its first control. */
  initialFocus?: readonly string[];
  children: ReactNode;
}) {
  const { t } = useI18n();
  const panel = useRef<HTMLDivElement | null>(null);
  const titleId = useRef(`game-dialog-${Math.random().toString(36).slice(2)}`).current;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    const first = [...(initialFocus ?? []), "button:not(:disabled), textarea, input, [href]"]
      .map((selector) => panel.current?.querySelector<HTMLElement>(selector))
      .find(Boolean);
    first?.focus();
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);
  return createPortal(
    <div className="confirm-backdrop game-dialog-backdrop" onClick={onClose}>
      <div
        ref={panel}
        className="game-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="game-dialog-head">
          <h2 id={titleId}>{title}</h2>
          <button type="button" className="game-dialog-close" onClick={onClose} aria-label={t("game.dialog.close")}>
            ×
          </button>
        </header>
        {children}
      </div>
    </div>,
    document.body,
  );
}
