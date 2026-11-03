import { afterEach, describe, expect, it, vi } from "vitest";
import { reloadWhenServerAnswers, serverAnswers } from "./preloadReload";

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// With the server stopped, opening a view whose chunk was not loaded yet
// failed twice, and the "a newer build is out" reload fired at once: the tab
// turned into the browser's own "127.0.0.1 refused to connect" page, the app
// and everything in it gone, and it stayed there after the server came back.
describe("reloadWhenServerAnswers", () => {
  it("waits for the server instead of reloading into a dead one", async () => {
    vi.useFakeTimers();
    const answers = [false, false, true];
    const probe = vi.fn(async () => answers.shift() ?? true);
    const reload = vi.fn();

    reloadWhenServerAnswers(probe, reload, 2_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(probe).toHaveBeenCalledTimes(1);
    expect(reload).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(2_000);
    expect(probe).toHaveBeenCalledTimes(2);
    expect(reload).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(2_000);
    expect(reload).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(20_000);
    expect(probe).toHaveBeenCalledTimes(3);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("reloads straight away when the server is there", async () => {
    const reload = vi.fn();
    reloadWhenServerAnswers(async () => true, reload, 2_000);
    await vi.waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  });
});

describe("serverAnswers", () => {
  it("is false when the request never gets an answer, true on any response", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await expect(serverAnswers()).resolves.toBe(false);
    fetch.mockResolvedValueOnce(new Response("", { status: 200 }));
    await expect(serverAnswers()).resolves.toBe(true);
  });
});
