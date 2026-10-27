import { describe, expect, it } from "vitest";
import {
  BUBBLE_MS,
  bubbleVisible,
  msUntilNextTick,
  PRESENCE_TICK_MS,
  presenceTick,
  SESSION_TTL_MS,
  sessionExpiresAt,
  sessionOnline,
} from "./presence";
import { NOW } from "./testFixtures";

describe("sessionOnline", () => {
  it("mirrors the hub's 90s session TTL", () => {
    expect(SESSION_TTL_MS).toBe(90_000);
  });

  it("is online inside the TTL and offline from the TTL on", () => {
    const at = (age: number) => sessionOnline({ online: true, lastSeen: NOW - age }, NOW);
    expect(at(0)).toBe(true);
    expect(at(89_999)).toBe(true);
    expect(at(90_000)).toBe(false);
    expect(at(600_000)).toBe(false);
  });

  it("believes the hub when it already says offline", () => {
    expect(sessionOnline({ online: false, lastSeen: NOW }, NOW)).toBe(false);
  });

  it("trusts the hub's online when no last-seen time came with it", () => {
    expect(sessionOnline({ online: true, lastSeen: 0 }, NOW)).toBe(true);
  });

  it("goes offline as the clock moves, without a new snapshot", () => {
    const session = { online: true, lastSeen: NOW };
    expect(sessionOnline(session, NOW + PRESENCE_TICK_MS)).toBe(true);
    expect(sessionOnline(session, NOW + 3 * PRESENCE_TICK_MS)).toBe(false);
  });
});

describe("presence tick", () => {
  it("buckets the clock into 30s windows", () => {
    expect(PRESENCE_TICK_MS).toBe(30_000);
    const start = Math.floor(NOW / 30_000) * 30_000;
    expect(presenceTick(start)).toBe(start);
    expect(presenceTick(start + 29_999)).toBe(start);
    expect(presenceTick(start + 30_000)).toBe(start + 30_000);
  });

  it("counts down to the next window, a whole step at a boundary", () => {
    const start = Math.floor(NOW / 30_000) * 30_000;
    expect(msUntilNextTick(start)).toBe(30_000);
    expect(msUntilNextTick(start + 1)).toBe(29_999);
    expect(msUntilNextTick(start + 29_999)).toBe(1);
    expect(msUntilNextTick(start + 10, 1_000)).toBe(990);
  });

  it("stays defined for a broken clock or step", () => {
    expect(presenceTick(Number.NaN)).toBe(0);
    expect(presenceTick(NOW, 0)).toBe(0);
    expect(msUntilNextTick(Number.NaN)).toBe(PRESENCE_TICK_MS);
    expect(msUntilNextTick(NOW, -1)).toBe(PRESENCE_TICK_MS);
  });

  it("says when a quiet session will show offline", () => {
    expect(sessionExpiresAt({ online: true, lastSeen: NOW - 10_000 }, NOW)).toBe(NOW + 80_000);
    expect(sessionExpiresAt({ online: true, lastSeen: NOW - 95_000 }, NOW)).toBeNull();
    expect(sessionExpiresAt({ online: false, lastSeen: NOW }, NOW)).toBeNull();
    expect(sessionExpiresAt({ online: true, lastSeen: 0 }, NOW)).toBeNull();
  });
});

describe("bubbleVisible", () => {
  it("shows a message for the bubble window after it was sent", () => {
    expect(bubbleVisible(NOW, NOW)).toBe(true);
    expect(bubbleVisible(NOW - BUBBLE_MS + 1, NOW)).toBe(true);
    expect(bubbleVisible(NOW - BUBBLE_MS, NOW)).toBe(false);
  });

  it("does not show a message from the future (a skewed clock) or without a time", () => {
    expect(bubbleVisible(NOW + 1_000, NOW)).toBe(false);
    expect(bubbleVisible(Number.NaN, NOW)).toBe(false);
  });
});
