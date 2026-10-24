import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FakeWebSocket } from "../runs/fakeWebSocket";
import { setProjectOverride } from "./http";
import { connectTerminal, MAX_TERM_CHUNK, terminalSocketURL, type TerminalHandlers } from "./terminal";

function handlers() {
  return {
    onReset: vi.fn(),
    onOutput: vi.fn(),
    onStatus: vi.fn(),
    onReadonly: vi.fn(),
    onState: vi.fn(),
  } satisfies TerminalHandlers;
}

const decode = (bytes: Uint8Array) => new TextDecoder().decode(bytes);

beforeEach(() => {
  vi.useFakeTimers();
  FakeWebSocket.reset();
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.spyOn(Math, "random").mockReturnValue(0.5);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setProjectOverride("");
});

describe("terminalSocketURL", () => {
  it("names the run and its project in the query", () => {
    expect(terminalSocketURL("run 1", "my project")).toBe(
      `ws://${location.host}/api/runs/run%201/terminal?project=my%20project`,
    );
  });

  it("falls back to this browser's project override, else no query", () => {
    expect(terminalSocketURL("r")).toBe(`ws://${location.host}/api/runs/r/terminal`);
    setProjectOverride("other");
    expect(terminalSocketURL("r")).toBe(`ws://${location.host}/api/runs/r/terminal?project=other`);
  });
});

describe("connectTerminal", () => {
  it("routes binary frames to output and text frames to status and read-only", () => {
    const h = handlers();
    connectTerminal("run1", "p", h);
    const socket = FakeWebSocket.last();
    expect(socket.binaryType).toBe("arraybuffer");
    expect(h.onState).toHaveBeenLastCalledWith("connecting");

    socket.serverOpen();
    expect(h.onReset).toHaveBeenCalledTimes(1);
    expect(h.onState).toHaveBeenLastCalledWith("open");

    socket.serverBinary("\x1b[1mhello\x1b[0m");
    expect(h.onOutput).toHaveBeenCalledTimes(1);
    expect(decode(h.onOutput.mock.calls[0][0])).toBe("\x1b[1mhello\x1b[0m");

    socket.serverText({ type: "status", status: "running" });
    expect(h.onStatus).toHaveBeenCalledWith("running");
    socket.serverText({ type: "readonly" });
    expect(h.onReadonly).toHaveBeenLastCalledWith(true);

    // Garbage and unknown controls are ignored.
    socket.onmessage?.({ data: "{not json" } as MessageEvent);
    socket.serverText({ type: "something-new" });
    expect(h.onOutput).toHaveBeenCalledTimes(1);
  });

  it("sends keystrokes as UTF-8 binary frames, split at the chunk cap", () => {
    const conn = connectTerminal("run1", "p", handlers());
    const socket = FakeWebSocket.last();
    expect(conn.sendInput("x")).toBe(false); // not open yet
    socket.serverOpen();

    expect(conn.sendInput("héllo\r")).toBe(true);
    expect(socket.input()).toEqual(["héllo\r"]);
    expect(socket.sent[0]).toBeInstanceOf(Uint8Array);

    socket.sent = [];
    conn.sendInput("a".repeat(MAX_TERM_CHUNK + 10));
    expect(socket.sent.map((item) => (item as Uint8Array).length)).toEqual([MAX_TERM_CHUNK, 10]);
  });

  it("drops keystrokes once the hub says read-only, and after the run ends", () => {
    const conn = connectTerminal("run1", "p", handlers());
    const socket = FakeWebSocket.last();
    socket.serverOpen();
    socket.serverText({ type: "readonly" });
    expect(conn.sendInput("ls\r")).toBe(false);
    expect(socket.sent).toEqual([]);

    const other = connectTerminal("run2", "p", handlers());
    const second = FakeWebSocket.last();
    second.serverOpen();
    second.serverText({ type: "status", status: "succeeded" });
    expect(other.sendInput("x")).toBe(false);
  });

  it("debounces resizes, clamps them and skips repeats", () => {
    const conn = connectTerminal("run1", "p", handlers(), { resizeDelayMs: 100 });
    const socket = FakeWebSocket.last();
    socket.serverOpen();

    conn.resize(100, 30);
    conn.resize(110, 31);
    conn.resize(120, 40);
    vi.advanceTimersByTime(99);
    expect(socket.controls()).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(socket.controls()).toEqual([{ type: "resize", cols: 120, rows: 40 }]);

    conn.resize(120, 40);
    vi.advanceTimersByTime(100);
    expect(socket.controls()).toHaveLength(1);

    conn.resize(9999, 0);
    vi.advanceTimersByTime(100);
    expect(socket.controls()[1]).toEqual({ type: "resize", cols: 500, rows: 1 });
  });

  it("reports the size on attach, but never for a read-only viewer", () => {
    const conn = connectTerminal("run1", "p", handlers(), { resizeDelayMs: 50 });
    conn.resize(90, 25);
    vi.advanceTimersByTime(50); // not open yet: kept for the attach
    const socket = FakeWebSocket.last();
    socket.serverOpen();
    expect(socket.controls()).toEqual([{ type: "resize", cols: 90, rows: 25 }]);

    socket.serverText({ type: "readonly" });
    conn.resize(60, 20);
    vi.advanceTimersByTime(50);
    expect(socket.controls()).toHaveLength(1);
  });

  it("reconnects with growing backoff while running, resetting the screen each time", () => {
    const h = handlers();
    connectTerminal("run1", "p", h);
    FakeWebSocket.last().serverOpen();
    FakeWebSocket.last().serverClose();
    expect(h.onState).toHaveBeenLastCalledWith("reconnecting");
    expect(FakeWebSocket.instances).toHaveLength(1);

    vi.advanceTimersByTime(749);
    expect(FakeWebSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(2);

    // A failed attempt (never opened) backs off further.
    FakeWebSocket.last().serverClose();
    vi.advanceTimersByTime(1124);
    expect(FakeWebSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeWebSocket.instances).toHaveLength(3);

    FakeWebSocket.last().serverOpen();
    expect(h.onReset).toHaveBeenCalledTimes(2);
    expect(h.onState).toHaveBeenLastCalledWith("open");
  });

  it("stops reconnecting once the run is final", () => {
    const h = handlers();
    connectTerminal("run1", "p", h);
    const socket = FakeWebSocket.last();
    socket.serverOpen();
    socket.serverText({ type: "status", status: "failed" });
    socket.serverClose(1000);
    expect(h.onState).toHaveBeenLastCalledWith("closed");
    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("cancels a pending retry when told the run ended", () => {
    const h = handlers();
    const conn = connectTerminal("run1", "p", h);
    FakeWebSocket.last().serverClose();
    conn.setFinal(true);
    expect(h.onState).toHaveBeenLastCalledWith("closed");
    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("attaches once for an already-final run", () => {
    connectTerminal("run1", "p", handlers(), { final: true });
    FakeWebSocket.last().serverOpen();
    FakeWebSocket.last().serverClose();
    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("gives up on a policy violation", () => {
    const h = handlers();
    connectTerminal("run1", "p", h);
    FakeWebSocket.last().serverClose(1008, "forbidden origin");
    expect(h.onState).toHaveBeenLastCalledWith("closed");
    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });

  it("closes cleanly without retrying", () => {
    const conn = connectTerminal("run1", "p", handlers());
    const socket = FakeWebSocket.last();
    socket.serverOpen();
    conn.close();
    expect(socket.close).toHaveBeenCalled();
    socket.serverClose();
    vi.advanceTimersByTime(60_000);
    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});
