/**
 * Copilot panel state.
 *
 * Its own small store rather than a slice of `useApp`: nothing outside the
 * panel reads it, and the app store's slices own persisted project concepts,
 * which a chat transcript is not. Turn events are folded in by the pure
 * `reduceTurnEvent`, so every event type can be tested without a network.
 */

import { create } from "zustand";
import type {
  CopilotConversation,
  CopilotEntry,
  CopilotMode,
  CopilotSource,
  CopilotStatus,
  CopilotToolKind,
  CopilotTurnEvent,
} from "../api";

export type ToolStatus = "running" | "awaiting" | "ok" | "error" | "denied";

export type TranscriptItem =
  | {
      kind: "user";
      id: string;
      text: string;
      source: CopilotSource;
      /** Shown before the server stored it; replaced by the stored entry. */
      live?: boolean;
    }
  | {
      kind: "assistant";
      id: string;
      text: string;
      /** Text is still arriving. */
      streaming?: boolean;
      live?: boolean;
    }
  | {
      kind: "tool";
      id: string;
      callId?: string;
      name: string;
      title: string;
      toolKind: CopilotToolKind | "";
      /** Runs in the browser (a `ui_*` tool). */
      client?: boolean;
      input: string;
      output: string;
      status: ToolStatus;
      live?: boolean;
    }
  | { kind: "notice"; id: string; text: string; tone: "info" | "error" };

export interface PermissionRequest {
  requestId: string;
  callId: string;
  turnId: string;
  name: string;
  /** Title from the matching tool_call, when one arrived first. */
  title: string;
  kind: CopilotToolKind;
  input: string;
  summary: string;
  /** Asked on every call: the full input is shown and "allow for this session" is not offered. */
  everyTime?: boolean;
  /** A decision was posted and the server has not confirmed it yet. */
  submitting?: boolean;
}

/**
 * needsRunner: the copilot is on, but its model calls wait for this person's runner.
 * needsDownload: the browser's built-in model is the choice, once downloaded.
 * needsSetup: automatic (free) mode, and no free model is available yet.
 */
export type ServerState = "unknown" | "disabled" | "needsRunner" | "needsDownload" | "needsSetup" | "ready";

/** The on-device model download: progress 0..1 while it runs. */
export interface BrowserModelDownload {
  active: boolean;
  progress: number | null;
  error: string | null;
}

export function serverStateOf(status: CopilotStatus | null): ServerState {
  if (!status) return "disabled";
  if (status.enabled) return "ready";
  if (status.needsRunner) return "needsRunner";
  if (status.needsDownload) return "needsDownload";
  if (status.needsSetup) return "needsSetup";
  return "disabled";
}

/** The part of the state a turn event can change. */
export interface TurnState {
  items: TranscriptItem[];
  pending: PermissionRequest[];
  running: boolean;
  turnId: string | null;
  /** Final assistant text of the newest finished turn, for spoken replies. */
  lastReply: { turnId: string; text: string } | null;
}

export interface CopilotPrefs {
  speakReplies: boolean;
  /** Mode a new conversation starts in. */
  defaultMode: CopilotMode;
}

export interface CopilotState extends TurnState, CopilotPrefs {
  serverState: ServerState;
  status: CopilotStatus | null;
  open: boolean;
  conversations: CopilotConversation[];
  activeId: string | null;
  mode: CopilotMode;
  /** How the newest turn was started; voice turns answer permissions by voice. */
  lastSource: CopilotSource;
  /** Transient line under the header (load failures, speech errors). */
  notice: string | null;
  download: BrowserModelDownload;

  setStatus: (status: CopilotStatus | null) => void;
  setOpen: (open: boolean) => void;
  toggleOpen: () => void;
  setConversations: (conversations: CopilotConversation[]) => void;
  showConversation: (conversation: CopilotConversation | null, entries: CopilotEntry[]) => void;
  /**
   * Shows a conversation a follow-up just landed in (a plan run ended) and
   * queues its reply to be spoken, as a finished turn's would be.
   */
  showFollowup: (conversation: CopilotConversation, entries: CopilotEntry[]) => void;
  /** The person has seen a conversation's follow-up. */
  markRead: (id: string) => void;
  setMode: (mode: CopilotMode) => void;
  setSpeakReplies: (speak: boolean) => void;
  setNotice: (notice: string | null) => void;
  setDownload: (download: Partial<BrowserModelDownload>) => void;
  /** Starts a turn: adds the optimistic user bubble. */
  beginTurn: (text: string, source: CopilotSource) => void;
  applyEvent: (event: CopilotTurnEvent) => void;
  /** Ends the turn on the client side (finished, aborted or failed). */
  endTurn: (error?: string) => void;
  markDecisionSubmitting: (requestId: string, submitting: boolean) => void;
}

const PREFS_KEY = "nodevas.copilot.prefs";

export function readCopilotPrefs(): CopilotPrefs {
  const defaults: CopilotPrefs = { speakReplies: false, defaultMode: "default" };
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (!raw) return defaults;
    const value = JSON.parse(raw) as Partial<CopilotPrefs>;
    return {
      speakReplies: typeof value.speakReplies === "boolean" ? value.speakReplies : false,
      defaultMode: isMode(value.defaultMode) ? value.defaultMode : "default",
    };
  } catch {
    return defaults;
  }
}

function writeCopilotPrefs(prefs: CopilotPrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
  } catch {
    /* private mode or blocked storage: the preference lasts this session */
  }
}

export function isMode(value: unknown): value is CopilotMode {
  return (
    value === "plan" ||
    value === "default" ||
    value === "acceptEdits" ||
    value === "bypassPermissions"
  );
}

let liveCounter = 0;
const liveId = (prefix: string) => `live-${prefix}-${++liveCounter}`;

/** Pretty JSON for display; strings that are already JSON are re-indented. */
export function formatToolValue(value: unknown): string {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value === "string") {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      return value;
    }
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function entryToItem(entry: CopilotEntry): TranscriptItem {
  switch (entry.kind) {
    case "user":
      return {
        kind: "user",
        id: entry.id,
        text: entry.text ?? "",
        source: entry.source === "voice" ? "voice" : "text",
      };
    case "assistant":
      return { kind: "assistant", id: entry.id, text: entry.text ?? "" };
    case "event":
      // Written by the server, not the person: a plan run ended.
      return { kind: "notice", id: entry.id, text: entry.text ?? "", tone: "info" };
    case "tool":
      return {
        kind: "tool",
        id: entry.id,
        name: entry.toolName ?? "",
        title: entry.toolName ?? "",
        toolKind: "",
        client: (entry.toolName ?? "").startsWith("ui_"),
        input: formatToolValue(entry.toolInput),
        output: entry.toolOutput ?? "",
        status: entry.toolStatus || "ok",
      };
    default:
      return { kind: "notice", id: entry.id, text: entry.text ?? "", tone: "info" };
  }
}

function finishStreaming(items: TranscriptItem[]): TranscriptItem[] {
  return items.some((item) => item.kind === "assistant" && item.streaming)
    ? items.map((item) =>
        item.kind === "assistant" && item.streaming ? { ...item, streaming: false } : item,
      )
    : items;
}

function updateTool(
  items: TranscriptItem[],
  callId: string,
  update: (item: Extract<TranscriptItem, { kind: "tool" }>) => TranscriptItem,
): TranscriptItem[] {
  return items.map((item) =>
    item.kind === "tool" && item.callId === callId ? update(item) : item,
  );
}

/**
 * Folds a stored entry into the transcript. The live bubbles built from the
 * stream are adopted (same text, now with the stored id) rather than shown
 * twice; an entry nothing on screen corresponds to is appended.
 */
/** An assistant entry with no text only called tools: the tool cards show it. */
function isSilentAssistant(entry: CopilotEntry): boolean {
  return entry.kind === "assistant" && !entry.text;
}

function adoptEntry(items: TranscriptItem[], entry: CopilotEntry): TranscriptItem[] {
  if (items.some((item) => item.id === entry.id)) return items;
  if (isSilentAssistant(entry)) return items;
  const stored = entryToItem(entry);
  const index = items.findIndex((item) => {
    if (!("live" in item) || !item.live || item.kind !== entry.kind) return false;
    if (item.kind === "tool") return item.name === (entry.toolName ?? "");
    return true;
  });
  if (index < 0) return [...items, stored];
  const current = items[index];
  let merged: TranscriptItem;
  if (current.kind === "tool" && stored.kind === "tool") {
    // The live card knows the title, kind and call id the entry lacks.
    merged = {
      ...current,
      id: stored.id,
      live: false,
      output: stored.output || current.output,
      input: current.input || stored.input,
      status: entry.toolStatus ? stored.status : current.status,
    };
  } else {
    merged = stored;
  }
  const next = items.slice();
  next[index] = merged;
  return next;
}

/** Applies one stream event. Pure: returns the changed fields only. */
export function reduceTurnEvent(state: TurnState, event: CopilotTurnEvent): Partial<TurnState> {
  switch (event.type) {
    case "turn_started": {
      const items = state.items.map((item) =>
        item.kind === "user" && item.live && !state.items.some((other) => other.id === event.entryId)
          ? { ...item, id: event.entryId || item.id, live: !event.entryId }
          : item,
      );
      return { turnId: event.turnId, running: true, items };
    }
    case "text_delta": {
      if (!event.text) return {};
      const last = state.items[state.items.length - 1];
      if (last?.kind === "assistant" && last.streaming) {
        return {
          items: [...state.items.slice(0, -1), { ...last, text: last.text + event.text }],
        };
      }
      return {
        items: [
          ...state.items,
          { kind: "assistant", id: liveId("assistant"), text: event.text, streaming: true, live: true },
        ],
      };
    }
    case "tool_call":
    case "client_tool_call": {
      const items = finishStreaming(state.items);
      const existing = items.find((item) => item.kind === "tool" && item.callId === event.callId);
      if (existing) return { items };
      const client = event.type === "client_tool_call";
      return {
        items: [
          ...items,
          {
            kind: "tool",
            id: liveId("tool"),
            callId: event.callId,
            name: event.name,
            title: event.type === "tool_call" ? event.title || event.name : event.name,
            toolKind: event.type === "tool_call" ? event.kind : "read",
            client: client || event.name.startsWith("ui_"),
            input: formatToolValue(event.input),
            output: "",
            status: "running",
            live: true,
          },
        ],
      };
    }
    case "permission_request": {
      if (state.pending.some((request) => request.requestId === event.requestId)) return {};
      const tool = state.items.find(
        (item): item is Extract<TranscriptItem, { kind: "tool" }> =>
          item.kind === "tool" && item.callId === event.callId,
      );
      return {
        items: updateTool(finishStreaming(state.items), event.callId, (item) => ({
          ...item,
          status: "awaiting",
        })),
        pending: [
          ...state.pending,
          {
            requestId: event.requestId,
            callId: event.callId,
            turnId: state.turnId ?? "",
            name: event.name,
            title: tool?.title || event.name,
            kind: event.kind,
            input: formatToolValue(event.input),
            summary: event.summary,
            everyTime: event.everyTime === true,
          },
        ],
      };
    }
    case "permission_resolved": {
      const request = state.pending.find((item) => item.requestId === event.requestId);
      const pending = state.pending.filter((item) => item.requestId !== event.requestId);
      if (!request) return { pending };
      const denied = event.decision === "deny";
      return {
        pending,
        items: updateTool(state.items, request.callId, (item) => ({
          ...item,
          status: denied ? "denied" : item.status === "awaiting" ? "running" : item.status,
        })),
      };
    }
    case "tool_result":
      return {
        items: updateTool(state.items, event.callId, (item) => ({
          ...item,
          output: event.output ?? "",
          status: item.status === "denied" ? "denied" : event.isError ? "error" : "ok",
        })),
      };
    case "entry":
      return event.entry ? { items: adoptEntry(state.items, event.entry) } : {};
    case "error":
      return {
        items: [
          ...finishStreaming(state.items),
          { kind: "notice", id: liveId("error"), text: event.message || event.code || "error", tone: "error" },
        ],
      };
    case "turn_finished": {
      const items = finishStreaming(state.items);
      // The reply to speak is the assistant text after this turn's user bubble.
      let lastUser = -1;
      items.forEach((item, index) => {
        if (item.kind === "user") lastUser = index;
      });
      const reply = items
        .slice(lastUser + 1)
        .filter((item): item is Extract<TranscriptItem, { kind: "assistant" }> => item.kind === "assistant")
        .map((item) => item.text)
        .join("\n\n")
        .trim();
      return {
        items,
        running: false,
        pending: [],
        lastReply: reply ? { turnId: state.turnId ?? liveId("turn"), text: reply } : state.lastReply,
        turnId: null,
      };
    }
    case "ping":
    case "model_request":
      // The browser runs its model call in session.ts; what the model says
      // comes back from the server as ordinary events.
      return {};
    default:
      // A newer server may send events this client does not know; ignore them.
      return {};
  }
}

const initialPrefs = readCopilotPrefs();

export const useCopilot = create<CopilotState>((set, get) => ({
  serverState: "unknown",
  status: null,
  open: false,
  conversations: [],
  activeId: null,
  mode: initialPrefs.defaultMode,
  speakReplies: initialPrefs.speakReplies,
  defaultMode: initialPrefs.defaultMode,
  lastSource: "text",
  notice: null,
  download: { active: false, progress: null, error: null },
  items: [],
  pending: [],
  running: false,
  turnId: null,
  lastReply: null,

  setStatus: (status) => set({ status, serverState: serverStateOf(status) }),
  setOpen: (open) => set({ open }),
  toggleOpen: () => set({ open: !get().open }),
  setConversations: (conversations) => set({ conversations }),
  showConversation: (conversation, entries) =>
    set({
      activeId: conversation?.id ?? null,
      mode: conversation && isMode(conversation.mode) ? conversation.mode : get().defaultMode,
      items: entries.filter((entry) => !isSilentAssistant(entry)).map(entryToItem),
      pending: [],
      lastReply: null,
    }),
  showFollowup: (conversation, entries) => {
    const reply = [...entries].reverse().find((entry) => entry.kind === "assistant");
    set({
      activeId: conversation.id,
      mode: isMode(conversation.mode) ? conversation.mode : get().defaultMode,
      items: entries.map(entryToItem),
      pending: [],
      lastReply: reply?.text ? { turnId: `followup-${reply.id}`, text: reply.text } : null,
    });
  },
  markRead: (id) =>
    set((state) => ({
      conversations: state.conversations.map((item) =>
        item.id === id && item.unread ? { ...item, unread: false } : item,
      ),
    })),
  setMode: (mode) => {
    const prefs = { speakReplies: get().speakReplies, defaultMode: mode };
    writeCopilotPrefs(prefs);
    set({ mode, defaultMode: mode });
  },
  setSpeakReplies: (speakReplies) => {
    writeCopilotPrefs({ speakReplies, defaultMode: get().defaultMode });
    set({ speakReplies });
  },
  setNotice: (notice) => set({ notice }),
  setDownload: (download) => set((state) => ({ download: { ...state.download, ...download } })),
  beginTurn: (text, source) =>
    set((state) => ({
      running: true,
      turnId: null,
      lastSource: source,
      notice: null,
      items: [...state.items, { kind: "user", id: liveId("user"), text, source, live: true }],
    })),
  applyEvent: (event) => set((state) => reduceTurnEvent(state, event)),
  endTurn: (error) =>
    set((state) => ({
      running: false,
      turnId: null,
      pending: [],
      items: [
        ...finishStreaming(state.items).map((item) =>
          item.kind === "tool" && (item.status === "running" || item.status === "awaiting")
            ? { ...item, status: "error" as const }
            : item,
        ),
        ...(error
          ? [{ kind: "notice" as const, id: liveId("error"), text: error, tone: "error" as const }]
          : []),
      ],
    })),
  markDecisionSubmitting: (requestId, submitting) =>
    set((state) => ({
      pending: state.pending.map((request) =>
        request.requestId === requestId ? { ...request, submitting } : request,
      ),
    })),
}));
