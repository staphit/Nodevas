import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { resetBrowserModelCache } from "./browserModel";
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
  resetBrowserModelCache();
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

  it("runs a model request on the browser's model and posts the raw answer", async () => {
    const prompts: { input: string; options: unknown }[] = [];
    vi.stubGlobal("LanguageModel", {
      availability: vi.fn(async () => "available"),
      create: vi.fn(async () => ({
        prompt: vi.fn(async (input: string, options: unknown) => {
          prompts.push({ input, options });
          return '{"type":"text","text":"你好"}';
        }),
        destroy: vi.fn(),
      })),
    });
    // The bypass mode the person left on does not apply to the browser's model.
    useCopilot.setState({
      activeId: "c1",
      mode: "bypassPermissions",
      status: {
        enabled: true,
        provider: "browser",
        model: "gemini-nano",
        modes: ["plan", "default", "acceptEdits"],
        allowBypass: false,
      },
    });
    const encoder = new TextEncoder();
    let release: () => void = () => undefined;
    const calls: { url: string; body: unknown }[] = [];
    const schema = { type: "object", properties: { name: { enum: ["get_node"] } } };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url, body: init?.body ? JSON.parse(init.body as string) : undefined });
        if (url.endsWith("/turns")) {
          // The server keeps the stream open until the browser answers.
          const body = new ReadableStream<Uint8Array>({
            start(controller) {
              const line = (event: unknown) => controller.enqueue(encoder.encode(JSON.stringify(event) + "\n"));
              line({ type: "turn_started", turnId: "t1", entryId: "e1" });
              line({
                type: "model_request",
                requestId: "m1",
                modelRequest: { system: "sys", messages: [{ role: "user", content: "hi" }], schema, attempt: 1 },
              });
              release = () => {
                line({ type: "text_delta", text: "你好" });
                line({ type: "turn_finished", stop: "end_turn" });
                controller.close();
              };
            },
          });
          return new Response(body, { status: 200 });
        }
        if (url.endsWith("/model-results")) {
          release();
          return new Response(JSON.stringify({ ok: true }));
        }
        return new Response(JSON.stringify({ conversations: [] }));
      }),
    );

    await sendTurn("hi", "text");

    const turn = calls.find((call) => call.url.endsWith("/turns"));
    expect(turn?.body).toMatchObject({ mode: "default", browserModel: "available" });
    expect(prompts).toEqual([{ input: "hi", options: expect.objectContaining({ responseConstraint: schema }) }]);
    expect(calls.find((call) => call.url.endsWith("/model-results"))).toEqual({
      url: "/api/copilot/conversations/c1/turns/t1/model-results",
      body: { requestId: "m1", output: '{"type":"text","text":"你好"}' },
    });
    expect(useCopilot.getState().lastReply).toEqual({ turnId: "t1", text: "你好" });
  });
});
