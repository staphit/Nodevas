import { afterEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "./explorer";

afterEach(() => {
  explorerApi.forgetRepoReads();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const listing = () =>
  new Response(JSON.stringify({ path: "src", entries: [], hidden: 0, truncated: false }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

// Clicking a folder open, shut and open again, or two panes asking for the
// same folder at once, is one request to the runner.
describe("repo reads", () => {
  it("shares one request between callers asking at the same time", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => listing());
    await Promise.all([
      explorerApi.repoTree("rn1", "r1", "src"),
      explorerApi.repoTree("rn1", "r1", "src"),
      explorerApi.repoTree("rn1", "r1", "src"),
    ]);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("answers a folder opened again from what it already has", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => listing());
    await explorerApi.repoTree("rn1", "r1", "src");
    await explorerApi.repoTree("rn1", "r1", "src");
    await explorerApi.repoFile("rn1", "r1", "src/a.js");
    await explorerApi.repoFile("rn1", "r1", "src/a.js");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("asks again after a refresh", async () => {
    const fetch = vi.spyOn(globalThis, "fetch").mockImplementation(async () => listing());
    await explorerApi.repoTree("rn1", "r1", "src");
    explorerApi.forgetRepoReads();
    await explorerApi.repoTree("rn1", "r1", "src");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("never keeps a refusal", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(async () => new Response(JSON.stringify({ code: "RUNNER_OFFLINE" }), { status: 503 }))
      .mockImplementation(async () => listing());
    await expect(explorerApi.repoTree("rn1", "r1", "src")).rejects.toThrow();
    await explorerApi.repoTree("rn1", "r1", "src");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("waits out a busy answer once instead of failing", async () => {
    const fetch = vi
      .spyOn(globalThis, "fetch")
      .mockImplementationOnce(
        async () => new Response(JSON.stringify({ error: "busy" }), { status: 429, headers: { "Retry-After": "0" } }),
      )
      .mockImplementation(async () => listing());
    await expect(explorerApi.repoTree("rn1", "r1", "src")).resolves.toMatchObject({ path: "src" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
