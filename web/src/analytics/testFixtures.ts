import type {
  AnalyticsSettings,
  Insights,
  RoutePick,
  ScorecardRow,
  Scorecards,
  Suggestion,
  TiersResponse,
} from "../api/analytics";

/** Test data shaped like the hub's analytics answers (fixture S1). */

export function scorecardRow(over: Partial<ScorecardRow> = {}): ScorecardRow {
  return {
    role: "executor",
    track: "all",
    agent: "claude",
    model: "sonnet",
    runs: 5,
    verdicts: 0,
    samples: 3,
    successRate: 0.6,
    reliability: 0.8,
    delivery: 0.6667,
    deliveryAdjusted: 0.7368,
    firstPassRate: 0.3333,
    reworkRounds: 0.3333,
    costUsd: 3,
    costPerDelivered: 1.5,
    costEstimated: false,
    medianSeconds: 600,
    tddRate: null,
    accuracy: null,
    score: 0.6253,
    tier: "probation",
    ...over,
  };
}

export function scorecards(rows: ScorecardRow[] = [scorecardRow()]): Scorecards {
  return { days: 30, since: "2026-09-29T00:00:00Z", minSamples: 10, rows };
}

export const cheaperSuggestion: Suggestion = {
  key: "cheaper_equal_tier:main:codex/gpt-5.5",
  kind: "cheaper_equal_tier",
  params: { role: "executor", saving: 40, rule: "main" },
  change: {
    type: "routing_rule_target",
    ruleId: "main",
    from: { agent: "claude", model: "sonnet" },
    to: { agent: "codex", model: "gpt-5.5" },
  },
};

export const testerFirstSuggestion: Suggestion = {
  key: "tester_first:main",
  kind: "tester_first",
  params: { rate: 33 },
  change: { type: "open_settings", row: "stages" },
};

export function insights(over: Partial<Insights> = {}): Insights {
  return {
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
    byRole: [
      { key: "executor", costUsd: 3, runs: 5, delivered: 2, costPerDelivered: 1.5 },
      { key: "reviewer", costUsd: 0.6, runs: 3, delivered: 2, costPerDelivered: 0.3 },
    ],
    byAgent: [],
    byModel: [],
    byNodeType: [{ key: "task", costUsd: 3.6, runs: 8, delivered: 2, costPerDelivered: 1.8 }],
    waste: {
      costUsd: 0.5,
      failedCostUsd: 0.5,
      retriedCostUsd: 0.1,
      abandonedCostUsd: 0.5,
      hotspots: [{ nodeId: "nC", nodeTitle: "Checkout retries", costUsd: 0.5, runs: 2 }],
    },
    suggestions: [cheaperSuggestion],
    ...over,
  };
}

export function tiers(over: Partial<TiersResponse> = {}): TiersResponse {
  return {
    days: 30,
    minSamples: 10,
    seniorMinSamples: 20,
    thresholds: { senior: 0.8, mid: 0.6 },
    required: { executor: "mid", tester: "mid", reviewer: "senior", qa: "senior", planner: "mid", researcher: "probation" },
    rows: [{ role: "executor", agent: "codex", model: "gpt-5.5", samples: 12, score: 0.9, tier: "mid" }],
    ...over,
  };
}

export function settings(over: Partial<AnalyticsSettings> = {}): AnalyticsSettings {
  return { project: "main", autoRouting: false, explorationPct: 10, requiredTiers: {}, ...over };
}

export function pick(over: Partial<RoutePick> = {}): RoutePick {
  return {
    id: "p1",
    project: "main",
    nodeId: "login",
    role: "executor",
    source: "auto",
    ruleId: "",
    runnerId: "rn1",
    agent: "codex",
    model: "gpt-5.5",
    tier: "mid",
    requiredTier: "mid",
    explore: false,
    reason: { code: "cheapest_meeting_tier" },
    runId: "",
    createdAt: "2026-10-28T10:00:00Z",
    ...over,
  };
}
