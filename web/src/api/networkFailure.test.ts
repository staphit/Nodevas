import { afterEach, describe, expect, it, vi } from "vitest";
import { isNetworkFailure, NetworkError, req } from "./http";

afterEach(() => {
  vi.restoreAllMocks();
  document.documentElement.lang = "";
});

// With the server stopped, every request failed with the browser's own
// TypeError, and "Failed to fetch" was what a Traditional Chinese window
// showed in its banner and status pill.
describe("a request the server never answered", () => {
  it("fails as a NetworkError worded in the page's language", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"));

    document.documentElement.lang = "zh-Hant-TW";
    const zh = await req("/api/graph").catch((error: unknown) => error);
    expect(zh).toBeInstanceOf(NetworkError);
    expect((zh as Error).message).toBe("無法連線到伺服器");

    document.documentElement.lang = "en";
    const en = await req("/api/graph").catch((error: unknown) => error);
    expect(en).toBeInstanceOf(NetworkError);
    expect((en as Error).message).toBe("Can't reach the server");
  });

  it("is recognised by its message, whichever language raised it", () => {
    expect(isNetworkFailure("無法連線到伺服器")).toBe(true);
    expect(isNetworkFailure("Can't reach the server")).toBe(true);
    expect(isNetworkFailure("Failed to fetch")).toBe(true);
    expect(isNetworkFailure("找不到節點")).toBe(false);
    expect(isNetworkFailure(undefined)).toBe(false);
  });
});
