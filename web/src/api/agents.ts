import type { GraphUser } from "../types";
import { getProjectOverride, req } from "./http";

/** "hold" parks every message to a session until a human approves it. */
export type AgentInbound = "open" | "hold";

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
  /** Absent on hubs older than the hold policy, which means "open". */
  inbound?: AgentInbound;
  /** The model the agent reported in its heartbeat. */
  model?: string;
  /** The session is a run's agent (a runner dispatched it), not a person's
   * own. Set by the hub from how the session connects. */
  run?: boolean;
  /** The run this session is the agent of, when the run's runner said so
   * through its proxy. Absent from older hubs and runners: match the run by
   * its session name then (game/joins.ts). */
  runId?: string;
}

/** Absent on hubs older than the hold policy, which means "delivered". */
export type AgentMessageStatus = "delivered" | "held" | "rejected";

/** A text file carried by a message, as the hub reports it. */
export interface AgentMessageFile {
  name: string;
  size: number;
  sha256: string;
}

/** A text file attached to an outgoing message. */
export interface AgentFileUpload {
  name: string;
  content: string;
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
  status?: AgentMessageStatus;
  notify?: boolean;
  files?: AgentMessageFile[];
  decidedBy?: string;
  decidedAt?: number;
}

export type AgentAskKind = "approval" | "question" | "choice" | "permission";
export type AgentAskStatus =
  | "pending"
  | "approved"
  | "denied"
  | "answered"
  | "expired";
export type AgentAskDecision =
  | "approve"
  | "approved"
  | "deny"
  | "denied"
  | "answer"
  | "approve_run";

export interface AgentAskAnswerRequest {
  decision: AgentAskDecision;
  note?: string;
  answer?: string;
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
  status: AgentAskStatus;
  decidedBy?: string;
  note?: string;
  createdAt: number;
  decidedAt?: number;
  expiresAt?: number;
  kind?: AgentAskKind;
  options?: string[];
  answer?: string;
  decision?: AgentAskDecision;
  runId?: string;
  tool?: string;
  toolInput?: string;
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
  /** Held messages the viewer may approve or reject. */
  getHeldAgentMessages: () =>
    req<{ messages: AgentMessage[] }>("/api/agents/messages?status=held"),
  approveAgentMessage: (id: string) =>
    req<AgentMessage>(`/api/agents/messages/${encodeURIComponent(id)}/approve`, {
      method: "POST",
    }),
  rejectAgentMessage: (id: string) =>
    req<AgentMessage>(`/api/agents/messages/${encodeURIComponent(id)}/reject`, {
      method: "POST",
    }),
  setAgentSessionInbound: (sessionId: string, inbound: AgentInbound) =>
    req<AgentSession>(
      `/api/agents/sessions/${encodeURIComponent(sessionId)}/inbound`,
      { method: "POST", body: JSON.stringify({ inbound }) },
    ),
  /**
   * Where a message's file downloads from. A plain link cannot carry the
   * X-Nodevas-Project header req() adds, so the project rides in the query;
   * the server reads ?project= before the header.
   */
  agentMessageFileURL: (messageId: string, name: string) => {
    const path = `/api/agents/messages/${encodeURIComponent(messageId)}/files/${encodeURIComponent(name)}`;
    const project = getProjectOverride();
    return project ? `${path}?project=${encodeURIComponent(project)}` : path;
  },
  sendAgentMessage: (payload: {
    to: string;
    message: string;
    replyTo?: string;
    nodeId?: string;
    notify?: boolean;
    files?: AgentFileUpload[];
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
  answerAgentAsk: (
    id: string,
    payload: AgentAskAnswerRequest | ("approved" | "denied" | "approve" | "deny"),
    legacyNote?: string,
  ) => {
    let body: AgentAskAnswerRequest;
    if (typeof payload === "string") {
      const decision: AgentAskDecision =
        payload === "approved" ? "approve" : payload === "denied" ? "deny" : payload;
      body = {
        decision,
        ...(legacyNote !== undefined && legacyNote !== "" ? { note: legacyNote } : {}),
      };
    } else {
      body = payload;
    }
    return req<{ ok: boolean; ask: AgentAsk }>(
      `/api/agents/asks/${encodeURIComponent(id)}/answer`,
      {
        method: "POST",
        body: JSON.stringify(body),
      },
    );
  },
};
