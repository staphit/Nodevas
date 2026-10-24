/**
 * Copilot commands: everything that talks to the server. Plain functions over
 * `useCopilot` (and `useApp` for the turn context) so components, hotkeys and
 * tests call the same thing.
 */

import {
  api,
  streamTurn,
  UnauthorizedError,
  type CopilotDecision,
  type CopilotMode,
  type CopilotSettings,
  type CopilotSource,
  type CopilotStatus,
  type CopilotTurnContext,
  type CopilotTurnEvent,
} from "../api";
import { translate } from "../i18n";
import { useApp } from "../store";
import { runClientTool, SUPPORTED_CLIENT_TOOLS } from "./clientTools";
import { useCopilot } from "./copilotStore";
import { cancelSpeech } from "./speech";

let controller: AbortController | null = null;
/** Set when the project changed under a running turn: end the conversation after it. */
let endAfterTurn = false;

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : translate("copilot.error.generic");

export function turnContext(): CopilotTurnContext {
  const app = useApp.getState();
  return {
    // The canvas selection is LaneView-local state; the open document is the
    // node the person is looking at, which is what the store knows.
    selectedNodeIds: app.activeTab ? [app.activeTab] : [],
    view: app.paneOpen.graph ? "graph" : "timeline",
    language: app.preferences.language,
  };
}

export async function refreshCopilotStatus(): Promise<void> {
  try {
    const status = await api.getCopilotStatus();
    useCopilot.getState().setStatus(status);
  } catch (error) {
    // 404 (no copilot on this server) and every other failure hide the button.
    if (error instanceof UnauthorizedError) return;
    useCopilot.getState().setStatus(null);
  }
}

/** The copilot's status, fetched fresh (the settings tab shows it). */
export async function fetchCopilotStatus(): Promise<CopilotStatus> {
  const status = await api.getCopilotStatus();
  useCopilot.getState().setStatus(status);
  return status;
}

/** The administrator's copilot settings; rejects for everyone else (403). */
export function loadCopilotSettings(): Promise<CopilotSettings> {
  return api.getCopilotSettings();
}

/** Where the copilot's key lives (administrators only). */
export async function saveCopilotKeySource(keySource: "auto" | "runner" | "server"): Promise<CopilotSettings> {
  const saved = await api.putCopilotSettings({ keySource });
  await refreshCopilotStatus();
  return saved;
}

/**
 * A runner came or went: when model calls run on runners, that may turn the
 * copilot on or off for this person.
 */
export function runnersChanged(): void {
  const { status, serverState } = useCopilot.getState();
  if (status?.keySource === "runner" || serverState === "needsRunner") void refreshCopilotStatus();
}

export async function loadConversations(): Promise<void> {
  const project = useApp.getState().activeProject;
  if (!project) return;
  try {
    const conversations = await api.listCopilotConversations(project);
    useCopilot.getState().setConversations(conversations);
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

export async function openConversation(id: string): Promise<void> {
  const copilot = useCopilot.getState();
  if (copilot.running || id === copilot.activeId) return;
  try {
    const { conversation, entries } = await api.getCopilotConversation(id);
    useCopilot.getState().showConversation(conversation, entries);
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

/** Clears the panel; the conversation itself is created by the first turn. */
export function newConversation(): void {
  if (useCopilot.getState().running) return;
  useCopilot.getState().showConversation(null, []);
}

export async function deleteConversation(id: string): Promise<void> {
  const copilot = useCopilot.getState();
  if (copilot.running && copilot.activeId === id) return;
  try {
    await api.deleteCopilotConversation(id);
    const state = useCopilot.getState();
    state.setConversations(state.conversations.filter((item) => item.id !== id));
    if (state.activeId === id) state.showConversation(null, []);
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

/** Modes the server offers this session, in cycling order. */
export function availableModes(): CopilotMode[] {
  const status = useCopilot.getState().status;
  const offered: CopilotMode[] = status?.modes?.length
    ? status.modes
    : ["plan", "default", "acceptEdits", "bypassPermissions"];
  return offered.filter((mode) => mode !== "bypassPermissions" || status?.allowBypass);
}

export async function changeMode(mode: CopilotMode): Promise<void> {
  const copilot = useCopilot.getState();
  if (copilot.mode === mode) return;
  copilot.setMode(mode);
  const id = copilot.activeId;
  if (!id) return;
  try {
    await api.updateCopilotConversation(id, { mode });
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

/** Shift+Tab: the next mode, wrapping, as in Claude Code. */
export function cycleMode(): void {
  const modes = availableModes();
  if (!modes.length) return;
  const index = modes.indexOf(useCopilot.getState().mode);
  void changeMode(modes[(index + 1) % modes.length]);
}

async function handleClientTool(
  conversationId: string,
  turnId: string | null,
  event: Extract<CopilotTurnEvent, { type: "client_tool_call" }>,
): Promise<void> {
  const result = await runClientTool(event.name, event.input);
  if (result.switchedProject) endAfterTurn = true;
  if (!turnId) return;
  try {
    await api.sendCopilotClientResult(conversationId, turnId, {
      callId: event.callId,
      output: result.output,
      isError: result.isError,
    });
  } catch (error) {
    useCopilot.getState().setNotice(errorText(error));
  }
}

function finishConversationIfEnded(): void {
  if (!endAfterTurn) return;
  endAfterTurn = false;
  useCopilot.getState().showConversation(null, []);
  useCopilot.getState().setConversations([]);
}

export async function sendTurn(raw: string, source: CopilotSource): Promise<void> {
  const text = raw.trim();
  const copilot = useCopilot.getState();
  if (!text || copilot.running) return;
  cancelSpeech();
  copilot.beginTurn(text, source);
  const project = useApp.getState().activeProject;
  let id = copilot.activeId;
  let finished = false;
  const abort = new AbortController();
  controller = abort;
  try {
    if (!id) {
      const conversation = await api.createCopilotConversation(project, copilot.mode);
      id = conversation.id;
      useCopilot.setState((state) => ({
        activeId: conversation.id,
        conversations: [conversation, ...state.conversations.filter((item) => item.id !== conversation.id)],
      }));
    }
    const conversationId = id;
    for await (const event of streamTurn(
      conversationId,
      {
        text,
        source,
        mode: useCopilot.getState().mode,
        context: turnContext(),
        clientTools: SUPPORTED_CLIENT_TOOLS,
      },
      abort.signal,
    )) {
      useCopilot.getState().applyEvent(event);
      if (event.type === "client_tool_call") {
        // Not awaited: the server keeps streaming (or waits) meanwhile.
        void handleClientTool(conversationId, useCopilot.getState().turnId, event);
      }
      if (event.type === "turn_finished") finished = true;
    }
    if (!finished) useCopilot.getState().endTurn(translate("copilot.error.streamEnded"));
  } catch (error) {
    if (abort.signal.aborted) {
      useCopilot.getState().endTurn(translate("copilot.stopped"));
    } else {
      useCopilot.getState().endTurn(errorText(error));
    }
  } finally {
    if (controller === abort) controller = null;
    finishConversationIfEnded();
    // The server may have titled the conversation from its first turn.
    void loadConversations();
  }
}

export async function stopTurn(): Promise<void> {
  cancelSpeech();
  const { activeId, turnId, running } = useCopilot.getState();
  if (!running) return;
  if (activeId && turnId) {
    await api.cancelCopilotTurn(activeId, turnId).catch(() => undefined);
  }
  controller?.abort();
}

export async function decide(
  requestId: string,
  decision: CopilotDecision,
  message?: string,
): Promise<void> {
  const copilot = useCopilot.getState();
  const request = copilot.pending.find((item) => item.requestId === requestId);
  const conversationId = copilot.activeId;
  if (!request || request.submitting || !conversationId) return;
  const turnId = request.turnId || copilot.turnId;
  if (!turnId) return;
  copilot.markDecisionSubmitting(requestId, true);
  try {
    await api.decideCopilotPermission(conversationId, turnId, {
      requestId,
      decision,
      ...(message ? { message } : {}),
    });
  } catch (error) {
    useCopilot.getState().markDecisionSubmitting(requestId, false);
    useCopilot.getState().setNotice(errorText(error));
  }
}

/**
 * The browser moved to another project. Conversations are per project, so the
 * panel starts over there — after the running turn, if one is running (a
 * `ui_switch_project` call is itself a running turn).
 */
export function projectChanged(): void {
  if (useCopilot.getState().running) {
    endAfterTurn = true;
    return;
  }
  endAfterTurn = false;
  useCopilot.getState().showConversation(null, []);
  useCopilot.getState().setConversations([]);
  void loadConversations();
}
