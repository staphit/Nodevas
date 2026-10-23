import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentMessage, AgentSession } from "../api";
import { setProjectOverride } from "../api";
import { agentsApi } from "../api/agents";
import {
  canManageSession,
  excerpt,
  resetAnnouncedForTests,
  useAgents,
} from "./agentStore";
import {
  addAttachments,
  MAX_AGENT_FILE_BYTES,
  MAX_AGENT_FILES,
  validAttachmentName,
} from "./attachments";

type Call = { url: string; method: string; body: unknown };

function message(over: Partial<AgentMessage> = {}): AgentMessage {
  return {
    id: "m1",
    project: "alpha",
    from: "alice/api-claude",
    senderActor: "alice",
    senderSession: "api-claude",
    senderAgent: "claude",
    to: "web-codex",
    body: "Schema ready",
    createdAt: 1700000000000,
    status: "delivered",
    ...over,
  };
}

const session: AgentSession = {
  id: "claude:1",
  project: "alpha",
  actorId: "alice",
  actorName: "alice",
  name: "api-claude",
  agent: "claude",
  state: "idle",
  since: 0,
  lastSeen: 0,
  online: true,
  inbound: "open",
};

/** Routes fetch by method + path; records every call. */
function mockFetch(routes: Record<string, (call: Call) => unknown>) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    const call = { url, method, body };
    calls.push(call);
    // An exact match (query included) wins over a path-only one.
    const key =
      routes[`${method} ${url}`] !== undefined
        ? `${method} ${url}`
        : Object.keys(routes).find((k) => k === `${method} ${url.split("?")[0]}`);
    if (!key) {
      return new Response(JSON.stringify({ error: "no route" }), { status: 404 });
    }
    const result = routes[key](call);
    if (result instanceof Response) return result;
    return new Response(JSON.stringify(result), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

const baseRoutes = {
  "GET /api/agents": () => ({ sessions: [session], users: [], unreadCount: 0 }),
  "GET /api/agents/asks": () => ({ asks: [] }),
};

beforeEach(() => {
  resetAnnouncedForTests();
  useAgents.setState({
    sessions: [],
    messages: [],
    asks: [],
    held: [],
    decidingIds: {},
    inboundPendingIds: {},
    toasts: [],
    viewer: null,
    filterNodeId: "",
    showOffline: false,
    error: null,
    sending: false,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  setProjectOverride("");
});

describe("held messages", () => {
  it("keeps only held messages from the held list", async () => {
    const heldMsg = message({ id: "h1", status: "held" });
    mockFetch({
      ...baseRoutes,
      "GET /api/agents/messages?status=held": () => ({
        // An old hub ignores ?status= and returns everything.
        messages: [heldMsg, message({ id: "d1" })],
      }),
      "GET /api/agents/messages": () => ({ messages: [message({ id: "d1" })] }),
    });

    await useAgents.getState().refreshAll();

    expect(useAgents.getState().held.map((m) => m.id)).toEqual(["h1"]);
    expect(useAgents.getState().sessions[0].inbound).toBe("open");
  });

  it("a held-list failure does not stop the rest refreshing", async () => {
    mockFetch({
      ...baseRoutes,
      "GET /api/agents/messages?status=held": () => new Response("{}", { status: 500 }),
      "GET /api/agents/messages": () => ({ messages: [message()] }),
    });
    await useAgents.getState().refreshAll();
    expect(useAgents.getState().messages).toHaveLength(1);
    expect(useAgents.getState().held).toEqual([]);
  });

  it("approve removes the message from held and updates it in the list", async () => {
    useAgents.setState({
      held: [message({ id: "h1", status: "held" })],
      messages: [message({ id: "h1", status: "held" })],
    });
    const calls = mockFetch({
      ...baseRoutes,
      "POST /api/agents/messages/h1/approve": () => message({ id: "h1", status: "delivered" }),
      "GET /api/agents/messages?status=held": () => ({ messages: [] }),
      "GET /api/agents/messages": () => ({ messages: [message({ id: "h1" })] }),
    });

    await useAgents.getState().decideHeld("h1", "approve");

    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/h1/approve"))).toBe(true);
    expect(useAgents.getState().held).toEqual([]);
    expect(useAgents.getState().messages[0].status).toBe("delivered");
    expect(useAgents.getState().decidingIds).toEqual({});
  });

  it("reject posts to /reject and marks the message rejected", async () => {
    useAgents.setState({
      held: [message({ id: "h2", status: "held" })],
      messages: [message({ id: "h2", status: "held" })],
    });
    const calls = mockFetch({
      ...baseRoutes,
      "POST /api/agents/messages/h2/reject": () => message({ id: "h2", status: "rejected" }),
      "GET /api/agents/messages?status=held": () => ({ messages: [] }),
      "GET /api/agents/messages": () => ({ messages: [message({ id: "h2", status: "rejected" })] }),
    });

    await useAgents.getState().decideHeld("h2", "reject");

    expect(calls.some((c) => c.url.endsWith("/h2/reject"))).toBe(true);
    expect(useAgents.getState().held).toEqual([]);
    expect(useAgents.getState().messages[0].status).toBe("rejected");
  });

  it("a failed decision shows the error and refetches what is still held", async () => {
    useAgents.setState({ held: [message({ id: "h3", status: "held" })] });
    const calls = mockFetch({
      "POST /api/agents/messages/h3/approve": () =>
        new Response(JSON.stringify({ error: "already decided" }), { status: 400 }),
      "GET /api/agents/messages?status=held": () => ({ messages: [] }),
    });

    await useAgents.getState().decideHeld("h3", "approve");
    await vi.waitFor(() => expect(useAgents.getState().held).toEqual([]));

    expect(useAgents.getState().error).toBe("already decided");
    expect(calls.filter((c) => c.url.includes("status=held"))).toHaveLength(1);
  });
});

describe("inbound", () => {
  it("setInbound posts the choice and applies the returned session", async () => {
    useAgents.setState({ sessions: [session] });
    const calls = mockFetch({
      "POST /api/agents/sessions/claude%3A1/inbound": () => ({ ...session, inbound: "hold" }),
    });

    await useAgents.getState().setInbound("claude:1", "hold");

    expect(calls[0].body).toEqual({ inbound: "hold" });
    expect(useAgents.getState().sessions[0].inbound).toBe("hold");
    expect(useAgents.getState().inboundPendingIds).toEqual({});
  });

  it("canManageSession: own sessions, any for admin, all without accounts", () => {
    expect(canManageSession(session, null)).toBe(true);
    expect(canManageSession(session, { id: "alice", name: "a", role: "member" })).toBe(true);
    expect(canManageSession(session, { id: "bob", name: "b", role: "member" })).toBe(false);
    expect(canManageSession(session, { id: "bob", name: "b", role: "admin" })).toBe(true);
  });
});

describe("send", () => {
  it("sends notify and files with the message", async () => {
    const calls = mockFetch({
      ...baseRoutes,
      "POST /api/agents/messages": () => ({ ok: true, threadId: "t", deliveries: [] }),
      "GET /api/agents/messages?status=held": () => ({ messages: [] }),
      "GET /api/agents/messages": () => ({ messages: [] }),
    });

    const ok = await useAgents.getState().sendMessage({
      to: "api-claude",
      message: " hi ",
      notify: true,
      files: [{ name: "plan.md", content: "# plan" }],
    });

    expect(ok).toBe(true);
    const post = calls.find((c) => c.method === "POST")!;
    expect(post.body).toEqual({
      to: "api-claude",
      message: "hi",
      notify: true,
      files: [{ name: "plan.md", content: "# plan" }],
    });
  });

  it("omits notify and files when unused", async () => {
    const calls = mockFetch({
      ...baseRoutes,
      "POST /api/agents/messages": () => ({ ok: true, threadId: "t", deliveries: [] }),
      "GET /api/agents/messages?status=held": () => ({ messages: [] }),
      "GET /api/agents/messages": () => ({ messages: [] }),
    });
    await useAgents.getState().sendMessage({ to: "*", message: "x", files: [] });
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ to: "*", message: "x" });
  });
});

describe("agent-notify", () => {
  it("toasts the message once, with an excerpt of its body", async () => {
    const long = "word ".repeat(60);
    const calls = mockFetch({
      "GET /api/agents/messages": () => ({
        messages: [message({ id: "n1", notify: true, body: long })],
      }),
    });

    await useAgents.getState().handleNotify("n1");
    await useAgents.getState().handleNotify("n1");

    const toasts = useAgents.getState().toasts;
    expect(toasts).toHaveLength(1);
    expect(toasts[0].from).toBe("alice/api-claude");
    expect(toasts[0].body.length).toBeLessThanOrEqual(140);
    expect(toasts[0].body.endsWith("…")).toBe(true);
    expect(calls).toHaveLength(1);
  });

  it("skips messages the viewer sent, and held ones", async () => {
    mockFetch({
      "GET /api/agents/messages": () => ({
        messages: [
          message({ id: "own", senderAgent: "human", senderActor: "bob", notify: true }),
          message({ id: "held", status: "held", notify: true }),
        ],
      }),
    });
    useAgents.setState({ viewer: { id: "bob", name: "bob", role: "member" } });

    await useAgents.getState().handleNotify("own");
    await useAgents.getState().handleNotify("held");
    await useAgents.getState().handleNotify("missing");

    expect(useAgents.getState().toasts).toEqual([]);
  });

  it("toasts dismiss themselves", async () => {
    vi.useFakeTimers();
    useAgents.getState().pushToast({ messageId: "m", from: "a", body: "b" });
    expect(useAgents.getState().toasts).toHaveLength(1);
    vi.advanceTimersByTime(8_000);
    expect(useAgents.getState().toasts).toHaveLength(0);
  });

  it("raises a system notification when permission is granted", async () => {
    const shown: { title: string; body?: string }[] = [];
    class FakeNotification {
      static permission = "granted";
      onclick: (() => void) | null = null;
      constructor(title: string, options?: NotificationOptions) {
        shown.push({ title, body: options?.body });
      }
      close() {}
    }
    vi.stubGlobal("Notification", FakeNotification);
    Object.defineProperty(document, "hidden", { value: true, configurable: true });
    mockFetch({
      "GET /api/agents/messages": () => ({ messages: [message({ id: "n2", notify: true })] }),
    });

    await useAgents.getState().handleNotify("n2");

    expect(shown).toEqual([{ title: "alice/api-claude", body: "Schema ready" }]);
    Object.defineProperty(document, "hidden", { value: false, configurable: true });
  });

  it("excerpt collapses whitespace", () => {
    expect(excerpt("a\n\n  b")).toBe("a b");
  });
});

describe("file download URL", () => {
  it("carries the project override in the query", () => {
    expect(agentsApi.agentMessageFileURL("m 1", "a b.md")).toBe(
      "/api/agents/messages/m%201/files/a%20b.md",
    );
    setProjectOverride("Side Project");
    expect(agentsApi.agentMessageFileURL("m1", "a.md")).toBe(
      "/api/agents/messages/m1/files/a.md?project=Side%20Project",
    );
  });
});

describe("attachments", () => {
  const text = (name: string, content: string) => new File([content], name, { type: "text/plain" });

  it("reads text files and enforces the hub limits", async () => {
    const big = new File([new Uint8Array(MAX_AGENT_FILE_BYTES + 1).fill(65)], "big.txt");
    const binary = new File([new Uint8Array([0x89, 0x50, 0x00, 0x47])], "img.png");
    const { files, errors } = await addAttachments(
      [],
      [text("a.md", "# A"), text(".env", "X=1"), big, binary, text("a.md", "again")],
    );
    expect(files).toEqual([{ name: "a.md", content: "# A" }]);
    expect(errors.map((e) => `${e.name}:${e.code}`)).toEqual([
      ".env:badName",
      "big.txt:tooLarge",
      "img.png:notText",
      "a.md:duplicate",
    ]);
  });

  it("accepts a file of exactly 256 KB", async () => {
    const exact = new File([new Uint8Array(MAX_AGENT_FILE_BYTES).fill(97)], "exact.txt");
    const { files, errors } = await addAttachments([], [exact]);
    expect(errors).toEqual([]);
    expect(files[0].content).toHaveLength(MAX_AGENT_FILE_BYTES);
  });

  it("stops at ten files", async () => {
    const current = Array.from({ length: MAX_AGENT_FILES - 1 }, (_, i) => ({
      name: `f${i}.txt`,
      content: "x",
    }));
    const { files, errors } = await addAttachments(current, [text("y.txt", "y"), text("z.txt", "z")]);
    expect(files).toHaveLength(MAX_AGENT_FILES);
    expect(errors).toEqual([{ name: "z.txt", code: "tooMany" }]);
  });

  it("validates names like the hub", () => {
    expect(validAttachmentName("notes.md")).toBe(true);
    expect(validAttachmentName("dir/notes.md")).toBe(false);
    expect(validAttachmentName("dir\\notes.md")).toBe(false);
    expect(validAttachmentName(".hidden")).toBe(false);
    expect(validAttachmentName("")).toBe(false);
    expect(validAttachmentName("a".repeat(128))).toBe(true);
    expect(validAttachmentName("a".repeat(129))).toBe(false);
  });
});
