import { create } from "zustand";
import {
  api,
  type AgentAsk,
  type AgentMessage,
  type AgentSession,
} from "../api";
import type { GraphUser } from "../types";

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
  submittingAskIds: Record<string, boolean>;
  sending: boolean;
  error: string | null;

  setOpen: (open: boolean) => void;
  toggleOpen: () => void;
  setShowOffline: (show: boolean) => void;
  setSelectedRecipient: (to: string) => void;
  setReplyToId: (id: string) => void;
  setFilterNodeId: (nodeId: string) => void;
  setError: (error: string | null) => void;
  refreshAll: () => Promise<void>;
  /** Messages bound to one board node; empty when they cannot be read. */
  loadNodeMessages: (nodeId: string) => Promise<AgentMessage[]>;
  sendMessage: (params: {
    to: string;
    message: string;
    replyTo?: string;
    nodeId?: string;
  }) => Promise<boolean>;
  answerAsk: (
    askId: string,
    decision: "approved" | "denied",
    note?: string,
  ) => Promise<void>;
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
  submittingAskIds: {},
  sending: false,
  error: null,

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

  refreshAll: async () => {
    const { showOffline, filterNodeId } = get();
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
  },

  loadNodeMessages: async (nodeId) => {
    try {
      const res = await api.getAgentMessages(nodeId);
      return res.messages ?? [];
    } catch {
      return [];
    }
  },

  sendMessage: async ({ to, message, replyTo, nodeId }) => {
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
      });
      set({ sending: false, replyToId: "" });
      await get().refreshAll();
      return true;
    } catch (err) {
      set({
        sending: false,
        error: err instanceof Error ? err.message : String(err),
      });
      return false;
    }
  },

  answerAsk: async (askId, decision, note) => {
    set((s) => ({
      submittingAskIds: { ...s.submittingAskIds, [askId]: true },
      error: null,
    }));
    try {
      await api.answerAgentAsk(askId, decision, note);
      await get().refreshAll();
    } catch (err) {
      set({ error: err instanceof Error ? err.message : String(err) });
    } finally {
      set((s) => {
        const next = { ...s.submittingAskIds };
        delete next[askId];
        return { submittingAskIds: next };
      });
    }
  },
}));
