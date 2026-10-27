import { afterEach, describe, expect, it, vi } from "vitest";
import { onUnauthorized, setProjectOverride, UnauthorizedError } from "./http";
import {
  copilotApi,
  NdjsonParser,
  parseTurnEventLine,
  readTurnEvents,
  streamTurn,
  type CopilotTurnEvent,
} from "./copilot";

function byteStream(chunks: (string | Uint8Array)[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(typeof chunk === "string" ? encoder.encode(chunk) : chunk);
      }
      controller.close();
    },
  });
}

async function collect(stream: AsyncIterable<CopilotTurnEvent>) {
  const events: CopilotTurnEvent[] = [];
  for await (const event of stream) events.push(event);
  return events;
}

afterEach(() => {
  setProjectOverride("");
  document.cookie = "nodevas_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT";
  vi.unstubAllGlobals();
});

describe("NdjsonParser", () => {
  it("keeps a line split across chunks until its newline arrives", () => {
    const parser = new NdjsonParser();
    expect(parser.push('{"type":"text_delta","te')).toEqual([]);
    expect(parser.push('xt":"hel')).toEqual([]);
    expect(parser.push('lo"}\n{"type":"text_delta","text":"!"}\n{"type":')).toEqual([
      { type: "text_delta", text: "hello" },
      { type: "text_delta", text: "!" },
    ]);
    expect(parser.push('"turn_finished","stop":"end"}')).toEqual([]);
    expect(parser.flush()).toEqual([{ type: "turn_finished", stop: "end" }]);
  });

  it("skips blank, malformed and unknown lines", () => {
    const parser = new NdjsonParser();
    expect(
      parser.push('\n\r\nnot json\n{"type":"mystery"}\n{"no":"type"}\n{"type":"error","message":"x"}\r\n'),
    ).toEqual([{ type: "error", message: "x" }]);
  });

  it("parses one line", () => {
    expect(parseTurnEventLine('{"type":"turn_started","turnId":"t1","entryId":"e1"}')).toEqual({
      type: "turn_started",
      turnId: "t1",
      entryId: "e1",
    });
    expect(parseTurnEventLine("")).toBeNull();
  });
});

describe("readTurnEvents", () => {
  it("decodes a multi-byte character split between chunks", async () => {
    const bytes = new TextEncoder().encode('{"type":"text_delta","text":"好"}\n');
    const cut = bytes.indexOf(0xe5) + 1; // inside the 3-byte 好
    const events = await collect(readTurnEvents(byteStream([bytes.slice(0, cut), bytes.slice(cut)])));
    expect(events).toEqual([{ type: "text_delta", text: "好" }]);
  });

  it("yields a final line that has no trailing newline", async () => {
    const events = await collect(
      readTurnEvents(byteStream(['{"type":"text_delta","text":"a"}\n{"type":"turn_finished","stop":"end"}'])),
    );
    expect(events.map((event) => event.type)).toEqual(["text_delta", "turn_finished"]);
  });
});

describe("streamTurn", () => {
  const request = {
    text: "hi",
    source: "text" as const,
    mode: "default" as const,
    context: { selectedNodeIds: [], view: "graph" as const, language: "en" },
    clientTools: [],
  };

  it("posts with the CSRF header and project override, and streams events", async () => {
    document.cookie = "nodevas_csrf=tok123";
    setProjectOverride("alpha");
    const fetchMock = vi.fn(
      async () =>
        new Response(byteStream(['{"type":"turn_started","turnId":"t","entryId":"e"}\n']), {
          status: 200,
          headers: { "Content-Type": "application/x-ndjson" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();
    const events = await collect(streamTurn("c 1", request, controller.signal));
    expect(events).toEqual([{ type: "turn_started", turnId: "t", entryId: "e" }]);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/copilot/conversations/c%201/turns");
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(init.signal).toBe(controller.signal);
    const headers = new Headers(init.headers);
    expect(headers.get("X-CSRF-Token")).toBe("tok123");
    expect(headers.get("X-Nodevas-Project")).toBe("alpha");
    expect(JSON.parse(init.body as string)).toEqual(request);
  });

  it("notifies the app on 401", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 401 })));
    const listener = vi.fn();
    const off = onUnauthorized(listener);
    await expect(collect(streamTurn("c", request))).rejects.toBeInstanceOf(UnauthorizedError);
    expect(listener).toHaveBeenCalledTimes(1);
    off();
  });

  it("surfaces the server's error message", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ error: "turn already running" }), { status: 409 })),
    );
    await expect(collect(streamTurn("c", request))).rejects.toThrow("turn already running");
  });
});

describe("copilotApi", () => {
  it("treats an empty success body as success for action endpoints", async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(copilotApi.cancelCopilotTurn("c", "t")).resolves.toBeUndefined();
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/copilot/conversations/c/turns/t/cancel");
    expect(init.method).toBe("POST");
  });

  it("accepts a bare array or a wrapped list of conversations", async () => {
    const conversation = { id: "a", project: "p", title: "", mode: "default", createdAt: 1, updatedAt: 2 };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ conversations: [conversation] }))));
    await expect(copilotApi.listCopilotConversations("p")).resolves.toEqual([conversation]);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify([conversation]))));
    await expect(copilotApi.listCopilotConversations("p")).resolves.toEqual([conversation]);
  });

  it("tells the status which built-in model this browser has", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ enabled: true })));
    vi.stubGlobal("fetch", fetchMock);
    await copilotApi.getCopilotStatus("downloadable");
    await copilotApi.getCopilotStatus();
    const urls = fetchMock.mock.calls.map((call) => (call as unknown as [string])[0]);
    expect(urls).toEqual(["/api/copilot/status?browserModel=downloadable", "/api/copilot/status"]);
  });

  it("posts the browser model's answer to the turn", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ ok: true })));
    vi.stubGlobal("fetch", fetchMock);
    await copilotApi.sendCopilotModelResult("c", "t", { requestId: "m1", output: '{"type":"text","text":"hi"}' });
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/copilot/conversations/c/turns/t/model-results");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ requestId: "m1", output: '{"type":"text","text":"hi"}' });
  });
});

describe("model_request events", () => {
  it("parses the browser model call", () => {
    const line = JSON.stringify({
      type: "model_request",
      requestId: "m1",
      modelRequest: { system: "s", messages: [{ role: "user", content: "hi" }], schema: { type: "object" }, attempt: 1 },
    });
    expect(parseTurnEventLine(line)).toMatchObject({ type: "model_request", requestId: "m1" });
  });
});
