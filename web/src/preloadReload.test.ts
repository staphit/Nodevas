import { describe, expect, it } from "vitest";
import {
  parseLastReload,
  PRELOAD_RELOAD_COOLDOWN_MS,
  PRELOAD_RETRY_WINDOW_MS,
  preloadChunkKey,
  shouldReloadForPreloadError,
} from "./preloadReload";

describe("preloadChunkKey", () => {
  it("names the chunk by the URL in the error", () => {
    expect(
      preloadChunkKey(
        new TypeError("Failed to fetch dynamically imported module: http://127.0.0.1:5666/assets/Board-abc123.js"),
      ),
    ).toBe("http://127.0.0.1:5666/assets/Board-abc123.js");
    expect(preloadChunkKey(new Error("Unable to preload CSS for /assets/Board-abc123.css"))).toBe(
      "/assets/Board-abc123.css",
    );
  });

  it("tells two chunks apart, and falls back to the message", () => {
    const a = preloadChunkKey(new TypeError("Failed to fetch dynamically imported module: http://h/assets/A-1.js"));
    const b = preloadChunkKey(new TypeError("Failed to fetch dynamically imported module: http://h/assets/B-2.js"));
    expect(a).not.toBe(b);
    expect(preloadChunkKey(new TypeError("Importing a module script failed."))).toBe(
      "Importing a module script failed.",
    );
    expect(preloadChunkKey(undefined)).toBe("");
  });
});

describe("shouldReloadForPreloadError", () => {
  const now = 10_000_000;

  it("lets a chunk's first failure through to its retry", () => {
    expect(shouldReloadForPreloadError(undefined, now, null)).toBe(false);
  });

  it("reloads when the same chunk's retry fails too", () => {
    expect(shouldReloadForPreloadError(now - 1_000, now, null)).toBe(true);
  });

  it("does not take an old failure of that chunk for its retry", () => {
    expect(shouldReloadForPreloadError(now - PRELOAD_RETRY_WINDOW_MS, now, null)).toBe(false);
  });

  it("reloads at most once per cooldown", () => {
    expect(shouldReloadForPreloadError(now - 1_000, now, now - 30_000)).toBe(false);
    expect(shouldReloadForPreloadError(now - 1_000, now, now - PRELOAD_RELOAD_COOLDOWN_MS + 1)).toBe(false);
    expect(shouldReloadForPreloadError(now - 1_000, now, now - PRELOAD_RELOAD_COOLDOWN_MS)).toBe(true);
  });

  it("counts a reload stamped in the future (the clock went back) as recent", () => {
    expect(shouldReloadForPreloadError(now - 1_000, now, now + 60_000)).toBe(false);
  });

  it("does not reload when two different chunks fail once each", () => {
    const failures = new Map<string, number>();
    const decide = (message: string, at: number) => {
      const chunk = preloadChunkKey(new TypeError(message));
      const previous = failures.get(chunk);
      failures.set(chunk, at);
      return shouldReloadForPreloadError(previous, at, null);
    };
    expect(decide("Failed to fetch dynamically imported module: http://h/assets/A-1.js", now)).toBe(false);
    expect(decide("Failed to fetch dynamically imported module: http://h/assets/B-2.js", now + 500)).toBe(false);
    expect(decide("Failed to fetch dynamically imported module: http://h/assets/A-1.js", now + 1_000)).toBe(true);
  });
});

describe("parseLastReload", () => {
  it("reads the stored time, and nothing or garbage as no reload yet", () => {
    expect(parseLastReload("1700000000000")).toBe(1_700_000_000_000);
    expect(parseLastReload(null)).toBeNull();
    expect(parseLastReload("")).toBeNull();
    expect(parseLastReload("soon")).toBeNull();
  });
});
