import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../store";
import { resyncAfterReconnect } from "../state/reconnect";
import { markServerReachable, useConnectivity } from "./connectivity";
import { req } from "./http";
import { connectWS } from "./ws";

// With the server stopped, nothing on the page said so for good: the error
// pill came and went with each failed request, and the runner badge stayed
// green. The page now keeps one "offline" fact, set when a request or the
// live socket cannot reach the server and cleared when either gets through.
beforeEach(() => markServerReachable());
afterEach(() => vi.restoreAllMocks());

describe("server reachability", () => {
  it("goes offline when a request never gets an answer, and back when one does", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"));
    await req("/api/graph").catch(() => undefined);
    expect(useConnectivity.getState().offline).toBe(true);

    fetch.mockResolvedValue(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
    await req("/api/graph");
    expect(useConnectivity.getState().offline).toBe(false);
  });

  it("stays online when the server answers with an error", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: "nope" }), { status: 500, headers: { "Content-Type": "application/json" } }),
    );
    await req("/api/graph").catch(() => undefined);
    expect(useConnectivity.getState().offline).toBe(false);
  });

  it("follows the live socket: a drop is offline, the reopen is online", () => {
    vi.useFakeTimers();
    const sockets: FakeSocket[] = [];
    class FakeSocket {
      static OPEN = 1;
      readyState = 0;
      onopen: (() => void) | null = null;
      onclose: ((event: { code: number; reason: string }) => void) | null = null;
      onmessage: ((m: { data: string }) => void) | null = null;
      constructor() {
        sockets.push(this);
      }
      send() {}
      close() {}
    }
    vi.stubGlobal("WebSocket", FakeSocket);
    try {
      const live = connectWS(() => {});
      sockets[0].onclose?.({ code: 1006, reason: "" });
      expect(useConnectivity.getState().offline).toBe(true);
      vi.advanceTimersByTime(20_000);
      sockets[1].onopen?.();
      expect(useConnectivity.getState().offline).toBe(false);
      live.close();
    } finally {
      vi.unstubAllGlobals();
      vi.useRealTimers();
    }
  });

  it("is cleared by the resync after a reconnect", async () => {
    useApp.setState({
      loadAll: vi.fn(async () => undefined),
      refreshState: vi.fn(async () => undefined),
      refreshTrash: vi.fn(async () => undefined),
      refreshProjects: vi.fn(async () => undefined),
    });
    useConnectivity.setState({ offline: true });
    await resyncAfterReconnect();
    expect(useConnectivity.getState().offline).toBe(false);
  });
});
