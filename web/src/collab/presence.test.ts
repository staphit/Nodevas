import { afterEach, describe, expect, it, vi } from "vitest";
import { decodePresence, encodePresence, throttled } from "./presence";

describe("decodePresence", () => {
  it("round-trips what this app sends", () => {
    const state = {
      nodeId: "node-0001",
      pageId: "notes",
      selection: { anchor: 3, head: 9 },
      pointer: { x: 12.5, y: -4 },
    };
    expect(decodePresence(encodePresence(state))).toEqual(state);
  });

  it("drops a caret that is not a document offset", () => {
    // This arrives from another browser and goes straight to CodeMirror.
    expect(
      decodePresence(JSON.stringify({ selection: { anchor: "3", head: 9 } })),
    ).toEqual({});
    expect(
      decodePresence(JSON.stringify({ selection: { anchor: -1, head: 9 } })),
    ).toEqual({});
    expect(
      decodePresence(JSON.stringify({ selection: { anchor: 1.5, head: 9 } })),
    ).toEqual({});
  });

  it("drops a pointer that is not a finite point", () => {
    expect(decodePresence(JSON.stringify({ pointer: { x: null, y: 1 } }))).toEqual({});
  });

  it("refuses malformed, oversized and missing payloads", () => {
    expect(decodePresence(undefined)).toBeNull();
    expect(decodePresence("not json")).toBeNull();
    expect(decodePresence("[]")).toEqual({});
    expect(decodePresence(JSON.stringify({ nodeId: "x".repeat(5000) }))).toBeNull();
  });

  it("ignores an id long enough to be an attack rather than a name", () => {
    expect(decodePresence(JSON.stringify({ nodeId: "x".repeat(300) }))).toEqual({});
  });
});

describe("throttled", () => {
  afterEach(() => vi.useRealTimers());

  it("sends the first value at once and the last one after the interval", () => {
    vi.useFakeTimers();
    const sent: number[] = [];
    const queue = throttled<number>((value) => sent.push(value), 50);

    queue.push(1);
    expect(sent).toEqual([1]);

    // A burst inside one interval keeps only its last value — the interesting
    // message is where the pointer stopped, not where it passed through.
    queue.push(2);
    queue.push(3);
    expect(sent).toEqual([1]);
    vi.advanceTimersByTime(60);
    expect(sent).toEqual([1, 3]);
  });

  it("forgets what it was going to send once cancelled", () => {
    vi.useFakeTimers();
    const sent: number[] = [];
    const queue = throttled<number>((value) => sent.push(value), 50);
    queue.push(1);
    queue.push(2);
    queue.cancel();
    vi.advanceTimersByTime(200);
    expect(sent).toEqual([1]);
  });
});
