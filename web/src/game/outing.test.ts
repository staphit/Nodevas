import { describe, expect, it } from "vitest";

import { fnv1a32, OUTING_KINDS, OUTING_STEPS, outingKindOf, outingOf } from "./outing";

describe("fnv1a32", () => {
  it("is the 32-bit FNV-1a hash", () => {
    expect(fnv1a32("")).toBe(0x811c9dc5);
    expect(fnv1a32("a")).toBe(0xe40c292c);
    expect(fnv1a32("foobar")).toBe(0xbf9cf968);
  });
});

describe("outingKindOf", () => {
  it("picks the same outing for the same run, every time", () => {
    expect(OUTING_KINDS).toEqual(["hike", "stream", "fish", "camp"]);
    expect(outingKindOf("run1")).toBe("stream");
    expect(outingKindOf("run2")).toBe("hike");
    expect(outingKindOf("run3")).toBe("camp");
    expect(outingKindOf("run4")).toBe("fish");
    expect(outingKindOf("65f0a1b2c3d4e5f600000002")).toBe("fish");
    expect(outingKindOf("run1")).toBe(outingKindOf("run1"));
  });
});

describe("outingOf", () => {
  const start = Date.parse("2026-11-05T12:00:00Z");
  const hour = 60 * 60 * 1000;
  const run = (over: Partial<{ id: string; createdAt: string; retryAt?: string }> = {}) => ({
    id: "run2",
    createdAt: new Date(start).toISOString(),
    retryAt: new Date(start + hour).toISOString(),
    ...over,
  });

  it("is as far along as the wait is, in whole steps", () => {
    expect(outingOf(run(), start)).toEqual({
      kind: "hike",
      step: 0,
      returnsAt: "2026-11-05T13:00:00.000Z",
      minutesLeft: 60,
    });
    expect(outingOf(run(), start + hour / 2)?.step).toBe(4);
    expect(outingOf(run(), start + hour / OUTING_STEPS - 1)?.step).toBe(0);
    expect(outingOf(run(), start + hour / OUTING_STEPS)?.step).toBe(1);
    expect(outingOf(run(), start + hour - 1)?.step).toBe(OUTING_STEPS - 1);
  });

  it("clamps to the first and the last step", () => {
    expect(outingOf(run(), start - hour)?.step).toBe(0);
    expect(outingOf(run(), start + hour)?.step).toBe(OUTING_STEPS - 1);
    expect(outingOf(run(), start + 5 * hour)?.step).toBe(OUTING_STEPS - 1);
  });

  it("rounds the minutes left up, never below zero", () => {
    expect(outingOf(run(), start + hour - 61_000)?.minutesLeft).toBe(2);
    expect(outingOf(run(), start + hour - 1)?.minutesLeft).toBe(1);
    expect(outingOf(run(), start + hour)?.minutesLeft).toBe(0);
    expect(outingOf(run(), start + 2 * hour)?.minutesLeft).toBe(0);
  });

  it("is on the way back when the reset is not after the wait began", () => {
    const early = run({ retryAt: new Date(start - 1_000).toISOString() });
    expect(outingOf(early, start)).toMatchObject({ step: OUTING_STEPS - 1, minutesLeft: 0 });
    const same = run({ retryAt: new Date(start).toISOString() });
    expect(outingOf(same, start)?.step).toBe(OUTING_STEPS - 1);
  });

  it("just left when the wait's start is unknown", () => {
    expect(outingOf(run({ createdAt: "" }), start + hour / 2)?.step).toBe(0);
  });

  it("is no outing without a reset time", () => {
    expect(outingOf(run({ retryAt: undefined }), start)).toBeUndefined();
    expect(outingOf(run({ retryAt: "" }), start)).toBeUndefined();
    expect(outingOf(run({ retryAt: "soon" }), start)).toBeUndefined();
  });
});
