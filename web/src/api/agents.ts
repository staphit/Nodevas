import type { GraphUser } from "../types";
import { req } from "./http";

export interface AgentSession {
  id: string;
  project: string;
  actorId: string;
  actorName: string;
  name: string;
  agent: string;
  role?: string;
  repo?: string;
  branch?: string;
  cwd?: string;
  state: "busy" | "idle" | string;
  since: number;
  doing?: string;
  nodeId?: string;
  lastSeen: number;
  online: boolean;
}

export interface AgentMessage {
  id: string;
  project: string;
  from: string;
  senderActor: string;
  senderSession?: string;
  senderAgent?: string;
  to: string;
  replyTo?: string;
  threadId?: string;
  nodeId?: string;
  body: string;
  createdAt: number;
  readAt?: number;
}

export interface AgentAsk {
  id: string;
  project: string;
  sessionId: string;
  sessionName: string;
  actorId: string;
  agent?: string;
  question: string;
  aboutMsgId?: string;
  nodeId?: string;
  status: "pending" | "approved" | "denied" | "expired";
  decidedBy?: string;
  note?: string;
  createdAt: number;
  decidedAt?: number;
}

export interface AgentRosterResponse {
  sessions: AgentSession[];
  users: GraphUser[];
  unreadCount: number;
}

export const agentsApi = {
  getAgentRoster: (all = false, filter = "") => {
    const params = new URLSearchParams();
    if (all) params.set("all", "true");
    if (filter) params.set("filter", filter);
    const qs = params.toString();
    return req<AgentRosterResponse>(`/api/agents${qs ? `?${qs}` : ""}`);
  },
  getAgentMessages: (nodeId?: string) => {
    const qs = nodeId ? `?nodeId=${encodeURIComponent(nodeId)}` : "";
    return req<{ messages: AgentMessage[] }>(`/api/agents/messages${qs}`);
  },
  sendAgentMessage: (payload: {
    to: string;
    message: string;
    replyTo?: string;
    nodeId?: string;
  }) =>
    req<{
      ok: boolean;
      threadId: string;
      deliveries: { recipient: string; status: string; messageId: string }[];
    }>("/api/agents/messages", {
      method: "POST",
      body: JSON.stringify(payload),
    }),
  getAgentAsks: (status?: string) => {
    const qs = status ? `?status=${encodeURIComponent(status)}` : "";
    return req<{ asks: AgentAsk[] }>(`/api/agents/asks${qs}`);
  },
  answerAgentAsk: (id: string, decision: "approved" | "denied", note?: string) =>
    req<{ ok: boolean; ask: AgentAsk }>(
      `/api/agents/asks/${encodeURIComponent(id)}/answer`,
      {
        method: "POST",
        body: JSON.stringify({ decision, note: note ?? "" }),
      },
    ),
};
