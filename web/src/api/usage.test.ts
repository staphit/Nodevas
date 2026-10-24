import { afterEach, describe, expect, it, vi } from "vitest";
import { normalizePrices, normalizeUsage, usageApi } from "./usage";

afterEach(() => {
  vi.restoreAllMocks();
});

function mockFetch(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
  );
}

describe("usage API", () => {
  it("fills every number the server leaves out", () => {
    const report = normalizeUsage({
      days: 7,
      totals: { inputTokens: 10, outputTokens: 5, reportedCostUsd: 0.5, estimatedCostUsd: 0.25 },
      byAgent: [{ key: "claude", inputTokens: 10 }],
      byModel: [{ key: "claude-opus-5-5", agent: "claude" }],
      byDay: null,
      sessions: [{ id: "s1", source: "run", models: ["a", 3] }, "junk"],
    });
    expect(report.totals).toMatchObject({
      inputTokens: 10,
      outputTokens: 5,
      cacheReadTokens: 0,
      totalTokens: 15,
      costUsd: 0.75,
      unpricedTokens: 0,
    });
    expect(report.currency).toBe("USD");
    expect(report.byAgent[0]).toMatchObject({ key: "claude", totalTokens: 10 });
    expect(report.byModel[0].agent).toBe("claude");
    expect(report.byDay).toEqual([]);
    expect(report.sessions).toHaveLength(1);
    expect(report.sessions[0]).toMatchObject({ id: "s1", source: "run", models: ["a"], name: "" });
  });

  it("keeps the server's own totals when it sends them", () => {
    const report = normalizeUsage({ totals: { inputTokens: 1, totalTokens: 99, costUsd: 3 } });
    expect(report.totals.totalTokens).toBe(99);
    expect(report.totals.costUsd).toBe(3);
  });

  it("asks for a clamped whole number of days", async () => {
    const fetch = mockFetch({ days: 365 });
    await usageApi.getUsage(9999);
    await usageApi.getUsage(0);
    await usageApi.getUsage(6.6);
    expect(fetch.mock.calls.map((c) => String(c[0]))).toEqual([
      "/api/usage?days=365",
      "/api/usage?days=1",
      "/api/usage?days=7",
    ]);
  });

  it("sends only the custom prices' fields on save", async () => {
    const fetch = mockFetch({ prices: [], asOf: "2026-10-06" });
    await usageApi.putUsagePrices([
      { model: " gpt-5.5 ", inputPerMTok: 1.25, outputPerMTok: 10, cacheReadPerMTok: 0.125, cacheWritePerMTok: 0 },
    ]);
    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe("/api/usage/prices");
    expect(init?.method).toBe("PUT");
    expect(JSON.parse(String(init?.body))).toEqual({
      prices: [
        { model: "gpt-5.5", inputPerMTok: 1.25, outputPerMTok: 10, cacheReadPerMTok: 0.125, cacheWritePerMTok: 0 },
      ],
    });
  });

  it("reads prices with a source", () => {
    const prices = normalizePrices({
      asOf: "2026-10-06",
      prices: [{ model: "a", inputPerMTok: 4, source: "custom" }, { model: "b", source: "weird" }],
    });
    expect(prices.asOf).toBe("2026-10-06");
    expect(prices.prices.map((p) => p.source)).toEqual(["custom", "builtin"]);
    expect(prices.prices[1].outputPerMTok).toBe(0);
  });
});
