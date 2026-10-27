import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiShapeError } from "./verify";
import {
  analyticsApi,
  StaleSuggestionError,
  verifyInsights,
  verifyScorecards,
  type Insights,
  type Scorecards,
} from "./analytics";

afterEach(() => vi.restoreAllMocks());

function mockFetch(body: unknown, status = 200) {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
    );
}

const urls = (fetch: ReturnType<typeof mockFetch>) => fetch.mock.calls.map((c) => String(c[0]));
const bodies = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.map((c) => JSON.parse(String((c[1] as RequestInit | undefined)?.body ?? "null")));

const INSIGHTS: Insights = {
  days: 30,
  since: "2026-09-29T00:00:00Z",
  currency: "USD",
  totals: {
    costUsd: 3.6,
    estimatedCostUsd: 0.6,
    runs: 8,
    units: 3,
    delivered: 2,
    costPerDelivered: 1.8,
    firstPassRate: 0.3333,
    reworkRounds: 0.3333,
  },
  byRole: [{ key: "executor", costUsd: 3, runs: 5, delivered: 2, costPerDelivered: 1.5 }],
  byAgent: [],
  byModel: [],
  byNodeType: [],
  waste: { costUsd: 0.5, failedCostUsd: 0.5, retriedCostUsd: 0.1, abandonedCostUsd: 0.5, hotspots: [] },
  suggestions: [],
};

const SCORECARDS: Scorecards = {
  days: 30,
  since: "2026-09-29T00:00:00Z",
  minSamples: 10,
  rows: [],
};

// W-01: URLs, shape checks, and the apply and dismiss bodies.
describe("analytics API", () => {
  it("asks for the project's insights, scorecards, tiers and settings", async () => {
    const fetch = mockFetch(INSIGHTS);
    await analyticsApi.insights(30);
    await analyticsApi.insights(7, "game one");
    // A fresh Response per call: a body can be read once.
    fetch.mockImplementation(async () => new Response(JSON.stringify(SCORECARDS)));
    await analyticsApi.scorecards(30, "code");
    await analyticsApi.scorecards(30, "all", "u-cal");
    expect(urls(fetch)).toEqual([
      "/api/analytics/insights?days=30",
      "/api/analytics/insights?days=7&project=game%20one",
      "/api/analytics/scorecards?days=30&track=code",
      "/api/analytics/scorecards?days=30&track=all&owner=u-cal",
    ]);
  });

  it("puts the settings and reads the picks of a project", async () => {
    const fetch = mockFetch({ project: "p", autoRouting: true, explorationPct: 10, requiredTiers: {} });
    await analyticsApi.putSettings({ project: "p", autoRouting: true, explorationPct: 10, requiredTiers: { executor: "mid" } }, "p");
    fetch.mockResolvedValue(new Response(JSON.stringify({ picks: [] })));
    await analyticsApi.routePicks(50, "p");
    expect(urls(fetch)).toEqual(["/api/analytics/settings?project=p", "/api/analytics/route-picks?limit=50&project=p"]);
    expect(fetch.mock.calls[0][1]?.method).toBe("PUT");
    expect(bodies(fetch)[0]).toEqual({ autoRouting: true, explorationPct: 10, requiredTiers: { executor: "mid" } });
  });

  it("posts exactly the key and the change it showed", async () => {
    const fetch = mockFetch({});
    const change = {
      type: "routing_rule_target" as const,
      ruleId: "main",
      from: { agent: "claude", model: "sonnet" },
      to: { agent: "codex", model: "gpt-5.5" },
    };
    await analyticsApi.applySuggestion("cheaper_equal_tier:main:codex/gpt-5.5", change);
    await analyticsApi.dismissSuggestion("tester_first:p", "p");
    expect(urls(fetch)).toEqual([
      "/api/analytics/suggestions/apply",
      "/api/analytics/suggestions/dismiss?project=p",
    ]);
    expect(bodies(fetch)).toEqual([{ key: "cheaper_equal_tier:main:codex/gpt-5.5", change }, { key: "tester_first:p" }]);
  });

  it("turns a 409 STALE_SUGGESTION into the new suggestion", async () => {
    const fresh = { key: "k", kind: "role_budget", params: {}, change: { type: "role_budget", role: "executor", dailyCostUsd: 9 } };
    mockFetch({ code: "STALE_SUGGESTION", error: "stale", suggestion: fresh }, 409);
    const err = await analyticsApi
      .applySuggestion("k", { type: "role_budget", role: "executor", dailyCostUsd: 7.2 })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(StaleSuggestionError);
    expect((err as StaleSuggestionError).suggestion).toEqual(fresh);
  });

  it("reads and writes the retention days", async () => {
    const fetch = mockFetch({ days: 180 });
    expect(await analyticsApi.retention()).toBe(180);
    await analyticsApi.putRetention(0);
    expect(urls(fetch)).toEqual(["/api/analytics/retention", "/api/analytics/retention"]);
    expect(bodies(fetch)[1]).toEqual({ days: 0 });
  });

  it("rejects malformed responses", () => {
    expect(verifyInsights(INSIGHTS, "/api/analytics/insights")).toEqual(INSIGHTS);
    expect(() => verifyInsights({ ...INSIGHTS, totals: null }, "/api/analytics/insights")).toThrow(ApiShapeError);
    expect(() => verifyInsights({ ...INSIGHTS, waste: { hotspots: "x" } }, "/api/analytics/insights")).toThrow(ApiShapeError);
    expect(verifyScorecards(SCORECARDS, "/api/analytics/scorecards")).toEqual(SCORECARDS);
    expect(() => verifyScorecards({ ...SCORECARDS, rows: {} }, "/api/analytics/scorecards")).toThrow(ApiShapeError);
    expect(() => verifyScorecards({ days: "30", rows: [] }, "/api/analytics/scorecards")).toThrow(ApiShapeError);
  });
});
