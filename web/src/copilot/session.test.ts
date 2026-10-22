import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { useCopilot } from "./copilotStore";
import { sendTurn } from "./session";

function ndjson(events: unknown[]): Response {
  const body = events.map((event) => JSON.stringify(event)).join("\n") + "\n";
  return new Response(body, { status: 200, headers: { "Content-Type": "application/x-ndjson" } });
}

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "zh-TW" },
    activeProject: "main",
    activeTab: "n1",
    paneOpen: { graph: true, timeline: true },
    graph: { version: 1, nodes: [{ id: "n1", title: "Login" }] } as never,
    openTab: vi.fn(async () => undefined),
    revealNode: vi.fn(),
  });
  useCopilot.setState({
    activeId: null,
    conversations: [],
    items: [],
    pending: [],
    running: false,
    turnId: null,
    mode: "default",
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sendTurn", () => {
  it("creates the conversation, streams the turn and answers a client tool", async () => {
    const calls: { url: string; body: unknown }[] = [];
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body ? JSON.parse(init.body as string) : undefined });
      if (url === "/api/copilot/conversations" && init?.method === "POST") {
        return new Response(
          JSON.stringify({ id: "c1", project: "main", title: "", mode: "default", createdAt: 1, updatedAt: 1 }),
        );
      }
      if (url === "/api/copilot/conversations/c1/turns") {
        return ndjson([
          { type: "turn_started", turnId: "t1", entryId: "e1" },
          { type: "client_tool_call", callId: "u1", name: "ui_open_node", input: { id: "n1" } },
          { type: "text_delta", text: "已開啟。" },
          { type: "turn_finished", stop: "end_turn" },
        ]);
      }
      if (url.endsWith("/client-results")) return new Response(JSON.stringify({ ok: true }));
      if (url.startsWith("/api/copilot/conversations?project=")) {
        return new Response(JSON.stringify({ conversations: [] }));
      }
      return new Response("{}", { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);

    await sendTurn("打開登入", "voice");
    await vi.waitFor(() => expect(calls.some((call) => call.url.endsWith("/client-results"))).toBe(true));

    const turn = calls.find((call) => call.url === "/api/copilot/conversations/c1/turns");
    expect(turn?.body).toEqual({
      text: "打開登入",
      source: "voice",
      mode: "default",
      context: { selectedNodeIds: ["n1"], view: "graph", language: "zh-TW" },
      clientTools: ["ui_open_node", "ui_set_view", "ui_switch_project"],
    });
    expect(calls.find((call) => call.url.endsWith("/client-results"))).toEqual({
      url: "/api/copilot/conversations/c1/turns/t1/client-results",
      body: { callId: "u1", output: "opened n1 (Login)", isError: false },
    });
    expect(useApp.getState().revealNode).toHaveBeenCalledWith("n1");
    const state = useCopilot.getState();
    expect(state.activeId).toBe("c1");
    expect(state.running).toBe(false);
    expect(state.lastReply).toEqual({ turnId: "t1", text: "已開啟。" });
    expect(state.items.map((item) => item.kind)).toEqual(["user", "tool", "assistant"]);
  });

  it("reports a stream that ends without turn_finished", async () => {
    useCopilot.setState({ activeId: "c1" });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) =>
        url.endsWith("/turns")
          ? ndjson([{ type: "turn_started", turnId: "t1", entryId: "e1" }])
          : new Response(JSON.stringify([])),
      ),
    );
    await sendTurn("hi", "text");
    const state = useCopilot.getState();
    expect(state.running).toBe(false);
    expect(state.items.at(-1)).toMatchObject({ kind: "notice", tone: "error" });
  });
});
