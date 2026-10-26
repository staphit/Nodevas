/**
 * Voice copilot endpoints (COPILOT.md, "HTTP API").
 *
 * Everything but the turn goes through `req()`. The turn is a streamed NDJSON
 * response to a POST, which neither `req()` (it parses one JSON body) nor
 * EventSource (GET only, no CSRF header) can read, so `streamTurn` does its
 * own fetch — with the same CSRF header, project override and 401 handling
 * `req()` applies, taken from the helpers http.ts exports.
 */

import {
  AuthError,
  csrfToken,
  getProjectOverride,
  notifyUnauthorized,
  req,
  UnauthorizedError,
} from "./http";

export type CopilotMode = "plan" | "default" | "acceptEdits" | "bypassPermissions";
export const COPILOT_MODES: readonly CopilotMode[] = [
  "plan",
  "default",
  "acceptEdits",
  "bypassPermissions",
];

/** Permission kind of a tool. Client (UI) tools are always `read`. */
export type CopilotToolKind = "read" | "write" | "risky";

export type CopilotSource = "voice" | "text";

export type CopilotDecision = "allow" | "allow_session" | "deny";

export type CopilotView = "graph" | "board" | "timeline";

export type CopilotClientTool =
  | "ui_open_node"
  | "ui_focus_nodes"
  | "ui_set_view"
  | "ui_set_filter"
  | "ui_switch_project";

export interface CopilotStatus {
  enabled: boolean;
  provider: string;
  model: string;
  modes: CopilotMode[];
  allowBypass: boolean;
  reason?: string;
  /** Where this person's model calls run: on the server, their own runner, or this browser. */
  keySource?: CopilotModelHost;
  /** The runner that makes them, when keySource is "runner". */
  runner?: string;
  /** All set but this person's runner: offline, or no copilot key on it. */
  needsRunner?: boolean;
  /** Chosen automatically: a free model (the browser's own, or a local server). */
  automatic?: boolean;
  /** Where the model runs. */
  modelHost?: CopilotModelHost;
  /** An OpenAI-compatible server found on the machine the model call runs on. */
  localServer?: "ollama" | "lmstudio";
  /** The server is loopback-only: it is this PC. */
  serverIsLocal?: boolean;
  /** The browser's built-in model is the choice, but must be downloaded first. */
  needsDownload?: boolean;
  /** Automatic mode, and nothing free is available here. */
  needsSetup?: boolean;
  reasonCode?: "no_model" | string;
}

/** The browser's Prompt API availability, as the server is told it. */
export type CopilotBrowserModel = "available" | "downloadable" | "downloading" | "unavailable";

/** Where the copilot's API key lives; "auto" is the server's when it has one. */
export type CopilotKeySource = "server" | "runner";

/** Where this person's model calls run (status.keySource): the browser needs no key at all. */
export type CopilotModelHost = CopilotKeySource | "browser";

export interface CopilotConversation {
  id: string;
  project: string;
  title: string;
  mode: CopilotMode;
  createdAt: number;
  updatedAt: number;
}

export type CopilotEntryKind = "user" | "assistant" | "tool" | "notice";

/** One stored transcript row: the display columns of `copilot_entries`. */
export interface CopilotEntry {
  id: string;
  conversationId?: string;
  seq: number;
  kind: CopilotEntryKind;
  source?: CopilotSource | "";
  /** Absent when empty: an assistant turn that only called tools. */
  text?: string;
  toolName?: string;
  toolInput?: string;
  toolOutput?: string;
  toolStatus?: "ok" | "error" | "denied" | "";
  createdAt: number;
}

export interface CopilotConversationDetail {
  conversation: CopilotConversation;
  entries: CopilotEntry[];
}

export interface CopilotTurnContext {
  selectedNodeIds: string[];
  view: CopilotView;
  language: string;
}

export interface CopilotTurnRequest {
  text: string;
  source: CopilotSource;
  mode: CopilotMode;
  context: CopilotTurnContext;
  clientTools: CopilotClientTool[];
  /** This browser's built-in model; the server uses it only when "available". */
  browserModel?: CopilotBrowserModel;
}

export interface CopilotSettings {
  /** "auto" picks a free model (the browser's, or a local server) when no key is set. */
  provider: "auto" | "anthropic" | "openai" | string;
  /** The administrator left the provider automatic (a key may still have resolved it). */
  automatic?: boolean;
  model: string;
  baseUrl: string;
  effort: string;
  allowBypass: boolean;
  /** The key itself is write-only; the server only says whether it has one. */
  hasApiKey?: boolean;
  apiKeySource?: "stored" | "environment" | "";
  /** The administrator's choice. */
  keySource?: CopilotKeySource | "auto";
  /** keySource with "auto" resolved. */
  effectiveKeySource?: CopilotKeySource;
  enabled?: boolean;
}

export type CopilotSettingsInput = Partial<
  Omit<CopilotSettings, "hasApiKey" | "apiKeySource" | "effectiveKeySource" | "enabled">
> & {
  /** Sent only to replace the stored key; never read back. */
  apiKey?: string;
};

// ---------------------------------------------------------------------------
// Turn stream events
// ---------------------------------------------------------------------------

export type CopilotTurnEvent =
  | { type: "turn_started"; turnId: string; entryId: string }
  | { type: "text_delta"; text: string }
  | {
      type: "tool_call";
      callId: string;
      name: string;
      title: string;
      kind: CopilotToolKind;
      input: unknown;
    }
  | {
      type: "permission_request";
      requestId: string;
      callId: string;
      name: string;
      kind: CopilotToolKind;
      input: unknown;
      summary: string;
      /** Asked on every call (dispatching tools): no "allow for this session". */
      everyTime?: boolean;
    }
  | { type: "permission_resolved"; requestId: string; decision: CopilotDecision | string }
  | { type: "client_tool_call"; callId: string; name: string; input: unknown }
  /** The browser must run this model call on its built-in model. */
  | { type: "model_request"; requestId: string; modelRequest: CopilotModelRequest }
  | { type: "tool_result"; callId: string; output: string; isError: boolean }
  | { type: "entry"; entry: CopilotEntry }
  | { type: "error"; message: string; code?: string }
  | { type: "turn_finished"; stop: string; usage?: Record<string, number> }
  /** Keeps a quiet stream (a turn parked on a permission card) alive through proxies. */
  | { type: "ping" };

/** One model call for the browser's built-in model (COPILOT.md, "Browser model"). */
export interface CopilotModelRequest {
  system: string;
  messages: { role: "user" | "assistant"; content: string }[];
  /** JSON Schema for the Prompt API's responseConstraint. */
  schema: Record<string, unknown>;
  attempt?: number;
}

export type CopilotTurnEventType = CopilotTurnEvent["type"];

const EVENT_TYPES = new Set<string>([
  "turn_started",
  "text_delta",
  "tool_call",
  "permission_request",
  "permission_resolved",
  "client_tool_call",
  "model_request",
  "tool_result",
  "entry",
  "error",
  "turn_finished",
]);

/** Parses one NDJSON line. Blank, malformed or unknown lines give null. */
export function parseTurnEventLine(line: string): CopilotTurnEvent | null {
  const trimmed = line.trim();
  if (!trimmed) return null;
  try {
    const value = JSON.parse(trimmed) as { type?: unknown };
    if (!value || typeof value !== "object" || typeof value.type !== "string") return null;
    if (!EVENT_TYPES.has(value.type)) return null;
    return value as CopilotTurnEvent;
  } catch {
    return null;
  }
}

/**
 * Incremental NDJSON splitter. A network chunk can end mid-line (or even
 * mid-character, which the TextDecoder in `readTurnEvents` handles); the tail
 * is kept until the newline that completes it arrives.
 */
export class NdjsonParser {
  private buffer = "";

  push(chunk: string): CopilotTurnEvent[] {
    this.buffer += chunk;
    const events: CopilotTurnEvent[] = [];
    let newline = this.buffer.indexOf("\n");
    while (newline >= 0) {
      const event = parseTurnEventLine(this.buffer.slice(0, newline));
      if (event) events.push(event);
      this.buffer = this.buffer.slice(newline + 1);
      newline = this.buffer.indexOf("\n");
    }
    return events;
  }

  /** The last line may arrive without a trailing newline. */
  flush(): CopilotTurnEvent[] {
    const rest = this.buffer;
    this.buffer = "";
    const event = parseTurnEventLine(rest);
    return event ? [event] : [];
  }
}

/** Reads a byte stream as turn events, in order. */
export async function* readTurnEvents(
  stream: ReadableStream<Uint8Array>,
): AsyncGenerator<CopilotTurnEvent> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  const parser = new NdjsonParser();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      for (const event of parser.push(decoder.decode(value, { stream: true }))) yield event;
    }
    for (const event of parser.push(decoder.decode())) yield event;
    for (const event of parser.flush()) yield event;
  } finally {
    reader.releaseLock();
  }
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const body = await res.json();
    if (body && typeof body.error === "string" && body.error) return body.error;
  } catch {
    /* keep status */
  }
  return `${res.status}`;
}

/**
 * Starts a turn and yields its events as they arrive. Aborting `signal` stops
 * reading (the server is told separately, through `cancelCopilotTurn`).
 */
export async function* streamTurn(
  conversationId: string,
  request: CopilotTurnRequest,
  signal?: AbortSignal,
): AsyncGenerator<CopilotTurnEvent> {
  const headers = new Headers({
    "Content-Type": "application/json",
    Accept: "application/x-ndjson",
  });
  const project = getProjectOverride();
  if (project) headers.set("X-Nodevas-Project", project);
  const token = csrfToken();
  if (token) headers.set("X-CSRF-Token", token);
  const res = await fetch(
    `/api/copilot/conversations/${encodeURIComponent(conversationId)}/turns`,
    {
      method: "POST",
      headers,
      body: JSON.stringify(request),
      credentials: "same-origin",
      signal,
    },
  );
  if (res.status === 401) {
    notifyUnauthorized();
    throw new UnauthorizedError();
  }
  if (!res.ok) {
    const message = await errorMessage(res);
    if (res.status === 403) throw new AuthError(403, message);
    throw new Error(message);
  }
  if (!res.body) throw new Error("empty response");
  yield* readTurnEvents(res.body);
}

// ---------------------------------------------------------------------------
// JSON endpoints
// ---------------------------------------------------------------------------

const base = "/api/copilot";
const conversationPath = (id: string) => `${base}/conversations/${encodeURIComponent(id)}`;
const turnPath = (id: string, turnId: string) =>
  `${conversationPath(id)}/turns/${encodeURIComponent(turnId)}`;

function conversationList(body: unknown): CopilotConversation[] {
  if (Array.isArray(body)) return body as CopilotConversation[];
  const list = (body as { conversations?: unknown } | null)?.conversations;
  return Array.isArray(list) ? (list as CopilotConversation[]) : [];
}

function conversationDetail(body: unknown): CopilotConversationDetail {
  const value = (body ?? {}) as {
    conversation?: CopilotConversation;
    entries?: CopilotEntry[];
  } & Partial<CopilotConversation>;
  const conversation = value.conversation ?? (value as CopilotConversation);
  return { conversation, entries: Array.isArray(value.entries) ? value.entries : [] };
}

/**
 * For the action endpoints. `req()` always parses a JSON body, so a 204 (or an
 * empty 200) surfaces as a SyntaxError from `res.json()` even though the call
 * succeeded; every real failure is thrown before that as a plain Error.
 */
async function reqAction(url: string, init: RequestInit): Promise<void> {
  try {
    await req<unknown>(url, init);
  } catch (error) {
    if (error instanceof SyntaxError) return;
    throw error;
  }
}

export const copilotApi = {
  getCopilotStatus: (browserModel?: CopilotBrowserModel) =>
    req<CopilotStatus>(
      browserModel ? `${base}/status?browserModel=${encodeURIComponent(browserModel)}` : `${base}/status`,
    ),

  listCopilotConversations: (project: string) =>
    req<unknown>(`${base}/conversations?project=${encodeURIComponent(project)}`).then(
      conversationList,
    ),

  createCopilotConversation: (project: string, mode: CopilotMode) =>
    req<unknown>(`${base}/conversations`, {
      method: "POST",
      body: JSON.stringify({ project, mode }),
    }).then((body) => conversationDetail(body).conversation),

  getCopilotConversation: (id: string) =>
    req<unknown>(conversationPath(id)).then(conversationDetail),

  deleteCopilotConversation: (id: string) =>
    reqAction(conversationPath(id), { method: "DELETE" }),

  updateCopilotConversation: (id: string, patch: { mode?: CopilotMode; title?: string }) =>
    req<unknown>(conversationPath(id), {
      method: "PATCH",
      body: JSON.stringify(patch),
    }).then((body) => conversationDetail(body).conversation),

  decideCopilotPermission: (
    id: string,
    turnId: string,
    decision: { requestId: string; decision: CopilotDecision; message?: string },
  ) =>
    reqAction(`${turnPath(id, turnId)}/decisions`, {
      method: "POST",
      body: JSON.stringify(decision),
    }),

  sendCopilotClientResult: (
    id: string,
    turnId: string,
    result: { callId: string; output: string; isError: boolean },
  ) =>
    reqAction(`${turnPath(id, turnId)}/client-results`, {
      method: "POST",
      body: JSON.stringify(result),
    }),

  sendCopilotModelResult: (
    id: string,
    turnId: string,
    result: { requestId: string; output?: string; error?: string },
  ) =>
    reqAction(`${turnPath(id, turnId)}/model-results`, {
      method: "POST",
      body: JSON.stringify(result),
    }),

  cancelCopilotTurn: (id: string, turnId: string) =>
    reqAction(`${turnPath(id, turnId)}/cancel`, { method: "POST" }),

  getCopilotSettings: () => req<CopilotSettings>(`${base}/settings`),

  putCopilotSettings: (settings: CopilotSettingsInput) =>
    req<CopilotSettings>(`${base}/settings`, {
      method: "PUT",
      body: JSON.stringify(settings),
    }),

  streamCopilotTurn: streamTurn,
};
