import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { coalesceRequest, COALESCE_WINDOW_MS, resetCoalescing } from "./coalesce";

/**
 * The batching window is a real timer, so every test here drives it by hand.
 * Waiting it out for real would make the suite slower for no extra confidence,
 * and would hide the difference between "one window" and "two windows", which
 * is exactly what several of these cases are about.
 */
beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  resetCoalescing();
  vi.useRealTimers();
});

/** Runs the microtask queue without letting the batching window elapse. */
async function settleMicrotasks(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

/** Opens the window, waits it out, and lets the fetch it starts settle. */
async function passWindow(): Promise<void> {
  await vi.advanceTimersByTimeAsync(COALESCE_WINDOW_MS);
}

/** A loader whose completion the test controls, so "in flight" is observable. */
function controllable() {
  const calls: Array<{ resolve: () => void; reject: (reason: unknown) => void }> = [];
  const load = vi.fn(
    () =>
      new Promise<void>((resolve, reject) => {
        calls.push({ resolve, reject });
      }),
  );
  return { load, calls };
}

describe("coalesceRequest", () => {
  // The common case: a mutation refreshes eagerly and the server's echo of the
  // same change asks for the refresh again a moment later. One read, not two.
  it("collapses two simultaneous callers into one request and answers both", async () => {
    const load = vi.fn().mockResolvedValue(undefined);

    const first = coalesceRequest("graph", load);
    const second = coalesceRequest("graph", load);
    await passWindow();

    await expect(first).resolves.toBeUndefined();
    await expect(second).resolves.toBeUndefined();
    expect(load).toHaveBeenCalledTimes(1);
  });

  // Resources are coalesced independently: a graph refresh must never be
  // answered by a trash read that happened to be gathered at the same moment.
  it("keeps separate resources on separate requests", async () => {
    const graph = vi.fn().mockResolvedValue(undefined);
    const trash = vi.fn().mockResolvedValue(undefined);

    const both = Promise.all([coalesceRequest("graph", graph), coalesceRequest("trash", trash)]);
    await passWindow();
    await both;

    expect(graph).toHaveBeenCalledTimes(1);
    expect(trash).toHaveBeenCalledTimes(1);
  });

  // The case that makes naive deduplication wrong. A request already on the
  // wire left before this change existed, so it cannot answer for it: joining
  // it would silently lose the edit until some unrelated event came along.
  it("gives a trigger that arrives mid-flight a fresh request of its own", async () => {
    const { load, calls } = controllable();

    const first = coalesceRequest("graph", load);
    await passWindow();
    expect(load).toHaveBeenCalledTimes(1);

    // Arrives while the first read is still on the wire.
    const second = coalesceRequest("graph", load);
    await passWindow();
    // Still one: the follow-up waits rather than overlapping the read in flight.
    expect(load).toHaveBeenCalledTimes(1);

    calls[0].resolve();
    await first;
    await settleMicrotasks();

    // Exactly one follow-up, and it started only once the first had finished.
    expect(load).toHaveBeenCalledTimes(2);
    calls[1].resolve();
    await expect(second).resolves.toBeUndefined();
  });

  // Several triggers landing during one long read are still one logical change
  // as far as the server is concerned; they must not queue up one read each.
  it("answers every trigger that arrives mid-flight with a single follow-up", async () => {
    const { load, calls } = controllable();

    const first = coalesceRequest("graph", load);
    await passWindow();

    const followers = [
      coalesceRequest("graph", load),
      coalesceRequest("graph", load),
      coalesceRequest("graph", load),
    ];
    await passWindow();

    calls[0].resolve();
    await first;
    await settleMicrotasks();
    expect(load).toHaveBeenCalledTimes(2);

    calls[1].resolve();
    await expect(Promise.all(followers)).resolves.toHaveLength(3);
    expect(load).toHaveBeenCalledTimes(2);
  });

  // Batching, not caching: triggers far enough apart describe changes the
  // earlier read cannot have seen, so each gets its own read.
  it("starts a second request for triggers that span two windows", async () => {
    const load = vi.fn().mockResolvedValue(undefined);

    const first = coalesceRequest("graph", load);
    await passWindow();
    await first;
    expect(load).toHaveBeenCalledTimes(1);

    const second = coalesceRequest("graph", load);
    await passWindow();
    await second;

    expect(load).toHaveBeenCalledTimes(2);
  });

  // Triggers spread across the window still cost one request: this is what
  // turns the observed burst of three reads for one node creation into one.
  it("gathers triggers spread across a single window into one request", async () => {
    const load = vi.fn().mockResolvedValue(undefined);

    // Three triggers inside the window, the last of them just before it closes.
    const step = Math.floor(COALESCE_WINDOW_MS / 3);
    const first = coalesceRequest("graph", load);
    await vi.advanceTimersByTimeAsync(step);
    const second = coalesceRequest("graph", load);
    await vi.advanceTimersByTimeAsync(step);
    const third = coalesceRequest("graph", load);
    await passWindow();

    await Promise.all([first, second, third]);
    expect(load).toHaveBeenCalledTimes(1);
  });

  // A key left marked busy by a failure would make every later refresh a no-op,
  // and the app would quietly stop updating until it was reloaded.
  it("clears the in-flight state when a request fails so the next one still fires", async () => {
    const load = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined);

    // The rejection is asserted before the window opens: the handler has to be
    // attached before the failure lands, or Node reports it as unhandled.
    const failed = expect(coalesceRequest("graph", load)).rejects.toThrow("offline");
    await passWindow();
    await failed;

    const retried = coalesceRequest("graph", load);
    await passWindow();

    await expect(retried).resolves.toBeUndefined();
    expect(load).toHaveBeenCalledTimes(2);
  });

  // A failure is shared with everyone who joined the batch, so no caller is
  // left waiting on a promise that will never settle.
  it("rejects every caller that joined a request that failed", async () => {
    const load = vi.fn().mockRejectedValue(new Error("offline"));

    const first = expect(coalesceRequest("graph", load)).rejects.toThrow("offline");
    const second = expect(coalesceRequest("graph", load)).rejects.toThrow("offline");
    await passWindow();

    await first;
    await second;
    expect(load).toHaveBeenCalledTimes(1);
  });

  // A follow-up must not inherit the failure of the read it was waiting behind:
  // it is a separate read, and it succeeded.
  it("runs the follow-up even when the request it waited for failed", async () => {
    const { load, calls } = controllable();

    const first = expect(coalesceRequest("graph", load)).rejects.toThrow("offline");
    await passWindow();
    const second = coalesceRequest("graph", load);
    await passWindow();

    calls[0].reject(new Error("offline"));
    await first;
    await settleMicrotasks();

    expect(load).toHaveBeenCalledTimes(2);
    calls[1].resolve();
    await expect(second).resolves.toBeUndefined();
  });

  // The reset runs between tests, and `writeGraph` awaits a refresh from inside
  // the operation it puts on `queues.graphSave` — module state that no reset
  // clears. So a waiter abandoned here is not merely leaked: it wedges the
  // single write lane for the rest of the process, and every graph write in
  // every later test in the file queues behind a promise that cannot settle.
  it("settles the callers it abandons when the module is reset", async () => {
    const { load, calls } = controllable();

    // Two ways a caller can be waiting on a batch that has not started, and the
    // reset throws both away: one still inside its window, and one gathered
    // behind a fetch already on the wire. A batch that has started is not at
    // risk — `execute` holds its own reference and settles it either way.
    const started = coalesceRequest("state", load);
    await passWindow();
    const gathering = coalesceRequest("graph", load);
    const behindTheFetch = coalesceRequest("state", load);

    resetCoalescing();

    await expect(gathering).resolves.toBeUndefined();
    await expect(behindTheFetch).resolves.toBeUndefined();
    calls[0].resolve();
    await expect(started).resolves.toBeUndefined();
  });

  // A reset drops the entry while its fetch is still in flight. When that fetch
  // finally lands it must not tidy up the entry a later trigger has since put
  // under the same key, which still has a timer pending and callers waiting.
  it("leaves a newer batch alone when an abandoned request finishes late", async () => {
    const { load, calls } = controllable();

    void coalesceRequest("graph", load).catch(() => undefined);
    await passWindow();
    expect(load).toHaveBeenCalledTimes(1);

    resetCoalescing();
    const afterReset = coalesceRequest("graph", load);
    // The abandoned fetch lands while the new batch is still gathering.
    calls[0].resolve();
    await settleMicrotasks();

    await passWindow();
    calls[1].resolve();
    await expect(afterReset).resolves.toBeUndefined();
    expect(load).toHaveBeenCalledTimes(2);
  });
});
