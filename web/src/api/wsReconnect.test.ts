import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { markServerReachable, useConnectivity } from "./connectivity";
import { connectWS } from "./ws";

// After the hub restarted, the page could sit offline for up to ~12 s: the
// socket's backoff had grown to its 10 s cap (plus jitter) while the hub was
// down. While offline the page now asks /api/healthz about once a second and
// reconnects the moment the hub answers. A socket that fails while the hub is
// up keeps backing off, so a broken /ws is not hammered.

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  /** While set, every new socket fails right after it is made. */
  static refuse = true;
  /** Called with each refused socket, before its close is delivered. */
  static onRefuse: ((socket: FakeSocket) => void) | null = null;
  readyState = 0;
  /** When it was made, on the fake clock. */
  at = Date.now();
  onopen: (() => void) | null = null;
  onclose: ((event: { code: number; reason: string }) => void) | null = null;
  onmessage: ((m: { data: string }) => void) | null = null;
  constructor() {
    FakeSocket.all.push(this);
    if (FakeSocket.refuse) {
      setTimeout(() => {
        FakeSocket.onRefuse?.(this);
        this.onclose?.({ code: 1006, reason: "" });
      }, 0);
    }
  }
  send() {}
  close() {}
}

let hubUp = false;
/** What a down hub looks like from here; a proxy in front answers for it. */
let whileDown: () => Response = () => {
  throw new TypeError("Failed to fetch");
};
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.useFakeTimers();
  markServerReachable();
  FakeSocket.all = [];
  FakeSocket.refuse = true;
  FakeSocket.onRefuse = null;
  hubUp = false;
  whileDown = () => {
    throw new TypeError("Failed to fetch");
  };
  vi.spyOn(Math, "random").mockReturnValue(0.5);
  vi.stubGlobal("WebSocket", FakeSocket);
  fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    if (!String(input).endsWith("/api/healthz")) throw new Error(`unexpected fetch ${String(input)}`);
    if (!hubUp) return whileDown();
    return new Response(JSON.stringify({ status: "ok", database: "ok" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

/** Lets failed reconnects run until the backoff has reached its cap. */
async function failUntilBackoffIsCapped() {
  await vi.advanceTimersByTimeAsync(60_000);
}

describe("reconnecting after the hub restarts", () => {
  it("reconnects within 2 s of the hub answering its health check", async () => {
    const live = connectWS(() => {});
    await failUntilBackoffIsCapped();
    expect(useConnectivity.getState().offline).toBe(true);

    // The hub comes back right after a refused attempt, so the backoff has
    // its full 10 s still to run.
    let backAt = -1;
    let attemptsWhenBack = 0;
    FakeSocket.onRefuse = () => {
      if (backAt >= 0) return;
      hubUp = true;
      FakeSocket.refuse = false;
      backAt = Date.now();
      attemptsWhenBack = FakeSocket.all.length;
    };
    await vi.advanceTimersByTimeAsync(10_500);
    expect(backAt).toBeGreaterThan(0);
    await vi.advanceTimersByTimeAsync(2_000);

    const next = FakeSocket.all[attemptsWhenBack];
    expect(next).toBeDefined();
    expect(next.at - backAt).toBeLessThanOrEqual(2_000);
    next.onopen?.();
    expect(useConnectivity.getState().offline).toBe(false);
    live.close();
  });

  it.each([
    ["a dev server's proxy", 500, "text/plain"],
    ["a reverse proxy", 502, "text/html"],
  ])("keeps asking while %s answers %i for the hub", async (_, status, contentType) => {
    whileDown = () => new Response("", { status, headers: { "Content-Type": contentType } });
    const live = connectWS(() => {});
    await failUntilBackoffIsCapped();
    let backAt = -1;
    let attemptsWhenBack = 0;
    FakeSocket.onRefuse = () => {
      if (backAt >= 0) return;
      hubUp = true;
      FakeSocket.refuse = false;
      backAt = Date.now();
      attemptsWhenBack = FakeSocket.all.length;
    };
    await vi.advanceTimersByTimeAsync(12_500);
    const next = FakeSocket.all[attemptsWhenBack];
    expect(next).toBeDefined();
    expect(next.at - backAt).toBeLessThanOrEqual(2_000);
    live.close();
  });

  it("stops asking once the socket is back", async () => {
    const live = connectWS(() => {});
    await vi.advanceTimersByTimeAsync(5_000);
    hubUp = true;
    FakeSocket.refuse = false;
    await vi.advanceTimersByTimeAsync(2_000);
    FakeSocket.all.at(-1)!.onopen?.();
    const asked = fetchMock.mock.calls.length;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(fetchMock.mock.calls.length).toBe(asked);
    live.close();
  });

  it("asks at most about once a second while the hub is down", async () => {
    const live = connectWS(() => {});
    await vi.advanceTimersByTimeAsync(30_000);
    expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(20);
    expect(fetchMock.mock.calls.length).toBeLessThanOrEqual(31);
    live.close();
  });

  it("keeps backing off when the socket fails while the hub is up", async () => {
    hubUp = true;
    const live = connectWS(() => {});
    await failUntilBackoffIsCapped();
    // Right after a refused attempt the next one is the full backoff away,
    // health check or not.
    let refusedAt = -1;
    FakeSocket.onRefuse = () => {
      if (refusedAt < 0) refusedAt = FakeSocket.all.length;
    };
    await vi.advanceTimersByTimeAsync(10_500);
    expect(refusedAt).toBeGreaterThan(0);
    const attempts = FakeSocket.all.length;
    FakeSocket.onRefuse = null;
    await vi.advanceTimersByTimeAsync(2_000);
    expect(FakeSocket.all.length).toBe(attempts);
    live.close();
  });

  it("stops asking once the page closes the socket", async () => {
    const live = connectWS(() => {});
    await vi.advanceTimersByTimeAsync(3_000);
    live.close();
    const asked = fetchMock.mock.calls.length;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchMock.mock.calls.length).toBe(asked);
  });
});
