import { afterEach, describe, expect, it, vi } from "vitest";
import { retryImport } from "./lazyWithRetry";

describe("retryImport", () => {
  afterEach(() => vi.useRealTimers());

  it("retries a failed import once after the delay", async () => {
    vi.useFakeTimers();
    const load = vi
      .fn<() => Promise<string>>()
      .mockRejectedValueOnce(new TypeError("Failed to fetch dynamically imported module"))
      .mockResolvedValueOnce("module");
    const result = retryImport(load, 1000);
    await vi.advanceTimersByTimeAsync(999);
    expect(load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await expect(result).resolves.toBe("module");
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("gives up after the retry fails, with the retry's error", async () => {
    vi.useFakeTimers();
    const load = vi
      .fn<() => Promise<string>>()
      .mockRejectedValueOnce(new Error("first"))
      .mockRejectedValueOnce(new Error("second"));
    const result = retryImport(load, 10);
    const settled = expect(result).rejects.toThrow("second");
    await vi.advanceTimersByTimeAsync(10);
    await settled;
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("does not retry an import that loaded", async () => {
    const load = vi.fn(() => Promise.resolve("module"));
    await expect(retryImport(load)).resolves.toBe("module");
    expect(load).toHaveBeenCalledTimes(1);
  });
});
