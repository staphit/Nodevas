import { create } from "zustand";
import {
  api,
  type Actor,
  type AgentAsk,
  type AgentAskAnswerRequest,
  type AgentAskDecision,
  type AgentFileUpload,
  type AgentInbound,
  type AgentMessage,
  type AgentSession,
} from "../api";
import type { GraphUser } from "../types";
import { translate } from "../i18n";
import { showSystemNotification } from "./systemNotify";

/** A notify=true message, shown briefly over the board. */
export interface AgentToast {
  id: string;
  messageId: string;
  from: string;
  body: string;
}

export const TOAST_MS = 8_000;
const MAX_TOASTS = 3;
const EXCERPT_CHARS = 140;

/** One line, cut at a word-ish boundary, for a toast or a notification. */
export function excerpt(body: string, max = EXCERPT_CHARS): string {
  const line = body.replace(/\s+/g, " ").trim();
  return line.length <= max ? line : `${line.slice(0, max - 1).trimEnd()}…`;
}

/**
 * Whether the viewer may hold or open a session's inbound, or decide its held
 * messages: their own sessions, or anyone's for an admin. A null viewer is a
 * loopback server with no accounts, where everything is one's own.
 */
export function canManageSession(
  session: Pick<AgentSession, "actorId">,
  viewer: Actor | null,
): boolean {
  return !viewer || viewer.role === "admin" || session.actorId === viewer.id;
}

/**
 * Whether the viewer may answer an ask. The hub says so on every ask it lists
 * (canAnswer), and that decides; only without it does the session rule apply,
 * with a permission prompt its runner owner's alone: answering it runs a tool
 * on their machine, which an administrator was never lent.
 */
export function canAnswerAsk(
  ask: Pick<AgentAsk, "actorId" | "kind" | "canAnswer">,
  viewer: Actor | null,
): boolean {
  if (typeof ask.canAnswer === "boolean") return ask.canAnswer;
  if (ask.kind === "permission") return !viewer || ask.actorId === viewer.id;
  return canManageSession(ask, viewer);
}

/** A message the viewer wrote themselves needs no notification. */
function sentByViewer(message: AgentMessage, viewer: Actor | null): boolean {
  if (message.senderAgent && message.senderAgent !== "human") return false;
  return !viewer || message.senderActor === viewer.id;
}

/** Where this browser remembers, per project, how far Agent chat was read. */
export function seenKey(project: string): string {
  return `nodevas-agent-chat-seen:${project}`;
}

function readSeen(project: string): number | null {
  try {
    const value = Number(localStorage.getItem(seenKey(project)));
    return Number.isFinite(value) && value > 0 ? value : null;
  } catch {
    return null;
  }
}

function writeSeen(project: string, at: number): void {
  try {
    localStorage.setItem(seenKey(project), String(at));
  } catch {
    /* this visit only */
  }
}

/** A message's time in ms (an older hub sent seconds). */
function messageTime(message: Pick<AgentMessage, "createdAt">): number {
  const at = message.createdAt || 0;
  return at < 1e12 ? at * 1000 : at;
}

/** Messages delivered after `seenAt` that the viewer did not write: what
 * Agent chat has unread. */
export function unseenCount(messages: AgentMessage[], seenAt: number, viewer: Actor | null): number {
  let count = 0;
  for (const message of messages) {
    if (message.status && message.status !== "delivered") continue;
    if (messageTime(message) > seenAt && !sentByViewer(message, viewer)) count++;
  }
  return count;
}

// Message ids already announced, so a repeated event never toasts twice.
const announced = new Set<string>();
let toastSeq = 0;

export interface AgentStoreState {
  open: boolean;
  showOffline: boolean;
  selectedRecipient: string;
  replyToId: string;
  filterNodeId: string;
  sessions: AgentSession[];
  users: GraphUser[];
  messages: AgentMessage[];
  asks: AgentAsk[];
  /** Held messages the viewer may approve or reject. */
  held: AgentMessage[];
  submittingAskIds: Record<string, boolean>;
  decidingIds: Record<string, boolean>;
  inboundPendingIds: Record<string, boolean>;
  toasts: AgentToast[];
  viewer: Actor | null;
  sending: boolean;
  error: string | null;
  /** Agent chat was read up to here (a message's createdAt), in this
   * browser, for `seenProject`. */
  seenAt: number;
  seenProject: string;

  setOpen: (open: boolean) => void;
  toggleOpen: () => void;
  setShowOffline: (show: boolean) => void;
  setSelectedRecipient: (to: string) => void;
  setReplyToId: (id: string) => void;
  setFilterNodeId: (nodeId: string) => void;
  setError: (error: string | null) => void;
  setViewer: (viewer: Actor | null) => void;
  /** Reads how far this browser read the project's Agent chat; the first
   * visit starts from now, so old messages do not count as unread. */
  loadSeen: (project: string) => void;
  /** Everything in the list now counts as read. */
  markSeen: () => void;
  refreshAll: () => Promise<void>;
  refreshHeld: () => Promise<void>;
  /** Messages bound to one board node; empty when they cannot be read. */
  loadNodeMessages: (nodeId: string) => Promise<AgentMessage[]>;
  sendMessage: (params: {
    to: string;
    message: string;
    replyTo?: string;
    nodeId?: string;
    notify?: boolean;
    files?: AgentFileUpload[];
  }) => Promise<boolean>;
  answerAsk: (
    askId: string,
    answer:
      | AgentAskAnswerRequest
      | AgentAskDecision
      | "approved"
      | "denied",
    note?: string,
  ) => Promise<boolean>;
  decideHeld: (messageId: string, decision: "approve" | "reject") => Promise<void>;
  setInbound: (sessionId: string, inbound: AgentInbound) => Promise<void>;
  /** Handles an "agent-notify" event: id is the message to announce. */
  handleNotify: (messageId: string) => Promise<void>;
  /** Handles an "agent-ask" event: id is a new question to announce to the
   * viewer when they may answer it. */
  handleAsk: (askId: string) => Promise<void>;
  pushToast: (toast: Omit<AgentToast, "id">) => void;
  dismissToast: (id: string) => void;
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** asks with one put in, replacing the same id. */
function mergeAsk(asks: AgentAsk[], ask: AgentAsk): AgentAsk[] {
  return [...asks.filter((a) => a.id !== ask.id), ask];
}

function replaceMessage(list: AgentMessage[], next: AgentMessage): AgentMessage[] {
  return list.map((m) => (m.id === next.id ? { ...m, ...next } : m));
}

export const useAgents = create<AgentStoreState>((set, get) => ({
  open: false,
  showOffline: false,
  selectedRecipient: "*",
  replyToId: "",
  filterNodeId: "",
  sessions: [],
  users: [],
  messages: [],
  asks: [],
  held: [],
  submittingAskIds: {},
  decidingIds: {},
  inboundPendingIds: {},
  toasts: [],
  viewer: null,
  sending: false,
  error: null,
  seenAt: 0,
  seenProject: "",

  setOpen: (open) => set({ open }),
  toggleOpen: () => set((s) => ({ open: !s.open })),
  setShowOffline: (showOffline) => {
    set({ showOffline });
    void get().refreshAll();
  },
  setSelectedRecipient: (selectedRecipient) => set({ selectedRecipient }),
  setReplyToId: (replyToId) => set({ replyToId }),
  setFilterNodeId: (filterNodeId) => {
    set({ filterNodeId });
    void get().refreshAll();
  },
  setError: (error) => set({ error }),
  setViewer: (viewer) => set({ viewer }),

  loadSeen: (project) => {
    let seenAt = readSeen(project);
    if (seenAt === null) {
      seenAt = Date.now();
      writeSeen(project, seenAt);
    }
    set({ seenAt, seenProject: project });
  },

  markSeen: () => {
    const { messages, seenAt, seenProject } = get();
    let newest = seenAt;
    for (const message of messages) newest = Math.max(newest, messageTime(message));
    if (newest <= seenAt) return;
    set({ seenAt: newest });
    if (seenProject) writeSeen(seenProject, newest);
  },

  refreshAll: async () => {
    const { showOffline, filterNodeId } = get();
    // Held messages load on their own: a hub without the hold policy answers
    // that route differently, and that must not stop the rest refreshing.
    const heldDone = get().refreshHeld();
    try {
      const [roster, msgRes, askRes] = await Promise.all([
        api.getAgentRoster(showOffline),
        api.getAgentMessages(filterNodeId || undefined),
        api.getAgentAsks(),
      ]);
      set((state) => {
        // Auto-select the first online session if current recipient is unset.
        let recipient = state.selectedRecipient;
        if (!recipient && roster.sessions.length > 0) {
          recipient = roster.sessions[0].name;
        }
        return {
          sessions: roster.sessions,
          users: roster.users,
          messages: msgRes.messages,
          asks: askRes.asks,
          selectedRecipient: recipient || "*",
          error: null,
        };
      });
    } catch {
      /* ignore transient fetch errors during background sync */
    }
    await heldDone;
  },

  refreshHeld: async () => {
    try {
      const res = await api.getHeldAgentMessages();
      // A hub that predates ?status= ignores it and lists everything; only
      // what is really held belongs here.
      set({ held: (res.messages ?? []).filter((m) => m.status === "held") });
    } catch {
      /* keep what is shown; the next change event retries */
    }
  },

  loadNodeMessages: async (nodeId) => {
    try {
      const res = await api.getAgentMessages(nodeId);
      return res.messages ?? [];
    } catch {
      return [];
    }
  },

  sendMessage: async ({ to, message, replyTo, nodeId, notify, files }) => {
    const trimmed = message.trim();
    const target = to.trim() || "*";
    if (!trimmed) return false;
    set({ sending: true, error: null });
    try {
      await api.sendAgentMessage({
        to: target,
        message: trimmed,
        replyTo: replyTo || undefined,
        nodeId: nodeId || undefined,
        notify: notify || undefined,
        files: files && files.length > 0 ? files : undefined,
      });
      set({ sending: false, replyToId: "" });
      await get().refreshAll();
      return true;
    } catch (err) {
      set({ sending: false, error: errorText(err) });
      return false;
    }
  },

  answerAsk: async (askId, answer, legacyNote) => {
    set((s) => ({
      submittingAskIds: { ...s.submittingAskIds, [askId]: true },
      error: null,
    }));
    try {
      const payload: AgentAskAnswerRequest =
        typeof answer === "string"
          ? {
              decision:
                answer === "approved"
                  ? "approve"
                  : answer === "denied"
                    ? "deny"
                    : answer,
              ...(legacyNote !== undefined && legacyNote !== ""
                ? { note: legacyNote }
                : {}),
            }
          : answer;
      await api.answerAgentAsk(askId, payload);
      await get().refreshAll();
      return true;
    } catch (err) {
      set({ error: errorText(err) });
      return false;
    } finally {
      set((s) => {
        const next = { ...s.submittingAskIds };
        delete next[askId];
        return { submittingAskIds: next };
      });
    }
  },

  decideHeld: async (messageId, decision) => {
    if (get().decidingIds[messageId]) return;
    set((s) => ({
      decidingIds: { ...s.decidingIds, [messageId]: true },
      error: null,
    }));
    try {
      const updated =
        decision === "approve"
          ? await api.approveAgentMessage(messageId)
          : await api.rejectAgentMessage(messageId);
      set((s) => ({
        held: s.held.filter((m) => m.id !== messageId),
        messages: updated?.id ? replaceMessage(s.messages, updated) : s.messages,
      }));
      void get().refreshAll();
    } catch (err) {
      set({ error: errorText(err) });
      // Someone else may have decided it first; show what is still held.
      void get().refreshHeld();
    } finally {
      set((s) => {
        const next = { ...s.decidingIds };
        delete next[messageId];
        return { decidingIds: next };
      });
    }
  },

  setInbound: async (sessionId, inbound) => {
    if (get().inboundPendingIds[sessionId]) return;
    set((s) => ({
      inboundPendingIds: { ...s.inboundPendingIds, [sessionId]: true },
      error: null,
    }));
    try {
      const updated = await api.setAgentSessionInbound(sessionId, inbound);
      set((s) => ({
        sessions: s.sessions.map((session) =>
          session.id === sessionId
            ? { ...session, ...(updated?.id ? updated : {}), inbound: updated?.inbound ?? inbound }
            : session,
        ),
      }));
    } catch (err) {
      set({ error: errorText(err) });
    } finally {
      set((s) => {
        const next = { ...s.inboundPendingIds };
        delete next[sessionId];
        return { inboundPendingIds: next };
      });
    }
  },

  handleNotify: async (messageId) => {
    if (!messageId || announced.has(messageId)) return;
    announced.add(messageId);
    if (announced.size > 500) announced.delete(announced.values().next().value!);
    let message: AgentMessage | undefined;
    try {
      // There is no single-message route; the project's list holds it, and
      // only what this viewer may see.
      const res = await api.getAgentMessages();
      message = (res.messages ?? []).find((m) => m.id === messageId);
      if (!get().filterNodeId && res.messages) set({ messages: res.messages });
    } catch {
      /* fall through: nothing to show */
    }
    if (!message || message.status === "held" || message.status === "rejected") return;
    if (sentByViewer(message, get().viewer)) return;
    const body = excerpt(message.body);
    get().pushToast({ messageId, from: message.from, body });
    showSystemNotification(message.from, body, `agent-msg-${messageId}`, () =>
      get().setOpen(true),
    );
  },

  handleAsk: async (askId) => {
    if (!askId || announced.has(`ask:${askId}`)) return;
    announced.add(`ask:${askId}`);
    let ask: AgentAsk | undefined;
    try {
      const res = await api.getAgentAsks("pending");
      ask = res.asks.find((a) => a.id === askId);
      if (ask) set({ asks: mergeAsk(get().asks, ask) });
    } catch {
      /* fall through: nothing to show */
    }
    if (!ask || ask.status !== "pending" || !canAnswerAsk(ask, get().viewer)) return;
    const sessionName = ask.sessionName || ask.agent || ask.sessionId;
    let toastFrom = sessionName;
    let notifyTitle = translate("agents.ask.notifyTitle", undefined, { session: sessionName });
    let bodyText = ask.question;

    if (ask.kind === "question") {
      toastFrom = translate("agents.ask.notifyQuestionTitle", undefined, { session: sessionName });
      notifyTitle = toastFrom;
    } else if (ask.kind === "choice") {
      toastFrom = translate("agents.ask.notifyChoiceTitle", undefined, { session: sessionName });
      notifyTitle = toastFrom;
    } else if (ask.kind === "permission") {
      toastFrom = translate("agents.ask.notifyPermissionTitle", undefined, {
        session: sessionName,
        tool: ask.tool || "tool",
      });
      notifyTitle = toastFrom;
      if (ask.toolInput) {
        bodyText = ask.toolInput;
      }
    }

    const body = excerpt(bodyText);
    get().pushToast({ messageId: `ask:${askId}`, from: toastFrom, body });
    showSystemNotification(notifyTitle, body,
      `agent-ask-${askId}`, () => get().setOpen(true));
  },

  pushToast: (toast) => {
    const id = `toast-${++toastSeq}`;
    set((s) => ({ toasts: [...s.toasts, { ...toast, id }].slice(-MAX_TOASTS) }));
    window.setTimeout(() => get().dismissToast(id), TOAST_MS);
  },

  dismissToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));

/** Test hook: forget which messages were announced. */
export function resetAnnouncedForTests(): void {
  announced.clear();
}
