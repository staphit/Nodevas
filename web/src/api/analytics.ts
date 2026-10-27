import type { AgentModel } from "./runs";

/**
 * Agent analytics (internal/analytics): insights, scorecards, tiers and
 * auto routing. Everything here is computed on the hub from run history with
 * plain SQL and fixed rules: no model is ever asked, no token spent.
 * Suggestions are never applied without the person confirming one.
 */

export type Tier = "senior" | "mid" | "probation";
export type Track = "all" | "code" | "story";

/** One (role, track, agent, model) key. A null metric had nothing to divide by. */
export interface ScorecardRow {
  role: string;
  track: Track;
  agent: string;
  model: string;
  runs: number;
  verdicts: number;
  samples: number;
  successRate: number | null;
  reliability: number | null;
  delivery: number | null;
  deliveryAdjusted: number | null;
  firstPassRate: number | null;
  reworkRounds: number | null;
  costUsd: number;
  costPerDelivered: number | null;
  costEstimated: boolean;
  medianSeconds: number | null;
  tddRate: number | null;
  accuracy: number | null;
  score: number;
  tier: Tier;
}

export interface Scorecards {
  days: number;
  since: string;
  minSamples: number;
  rows: ScorecardRow[];
}

export interface CostBucket {
  key: string;
  agent?: string;
  costUsd: number;
  runs: number;
  delivered: number;
  costPerDelivered: number | null;
}

export interface InsightTotals {
  costUsd: number;
  estimatedCostUsd: number;
  runs: number;
  units: number;
  delivered: number;
  costPerDelivered: number | null;
  firstPassRate: number | null;
  reworkRounds: number | null;
}

export interface WasteHotspot {
  nodeId: string;
  nodeTitle: string;
  costUsd: number;
  runs: number;
}

export interface Waste {
  costUsd: number;
  failedCostUsd: number;
  retriedCostUsd: number;
  abandonedCostUsd: number;
  hotspots: WasteHotspot[];
}

export type ChangeType = "routing_rule_target" | "role_budget" | "auto_routing" | "open_settings";

/** What applying a suggestion changes; the server checks it is still the
 * suggestion's before it applies anything. */
export interface SuggestionChange {
  type: ChangeType;
  ruleId?: string;
  from?: AgentModel;
  to?: AgentModel;
  role?: string;
  dailyCostUsd?: number;
  project?: string;
  on?: boolean;
  row?: string;
}

export type SuggestionKind =
  | "cheaper_equal_tier"
  | "demote_unreliable"
  | "enable_auto_routing"
  | "role_budget"
  | "tester_first";

export interface Suggestion {
  key: string;
  kind: SuggestionKind | string;
  params: Record<string, string | number>;
  change: SuggestionChange | null;
}

export interface Insights {
  days: number;
  since: string;
  currency: string;
  totals: InsightTotals;
  byRole: CostBucket[];
  byAgent: CostBucket[];
  byModel: CostBucket[];
  byNodeType: CostBucket[];
  waste: Waste;
  suggestions: Suggestion[];
}

export interface ProjectCost {
  key: string;
  costUsd: number;
  delivered: number;
  costPerDelivered: number | null;
}

export interface Overview {
  byProject: ProjectCost[];
}

export interface TierRow {
  role: string;
  agent: string;
  model: string;
  samples: number;
  score: number;
  tier: Tier;
}

export interface TiersResponse {
  days: number;
  minSamples: number;
  seniorMinSamples: number;
  thresholds: { senior: number; mid: number };
  required: Record<string, Tier>;
  rows: TierRow[];
}

export interface AnalyticsSettings {
  project: string;
  autoRouting: boolean;
  explorationPct: number;
  requiredTiers: Record<string, Tier>;
}

export type PickReasonCode =
  | "manual"
  | "cheapest_meeting_tier"
  | "explore"
  | "none_meets_tier"
  | "budget_exceeded"
  | "no_runner";

export interface RoutePick {
  id: string;
  project: string;
  nodeId: string;
  role: string;
  source: "manual" | "auto" | "none";
  ruleId: string;
  runnerId: string;
  agent: string;
  model: string;
  tier: string;
  requiredTier: string;
  explore: boolean;
  reason: { code: PickReasonCode | string; candidates?: unknown[] };
  runId: string;
  createdAt: string;
}

/** 409 STALE_SUGGESTION: the suggestion changed since it was shown. */
export class StaleSuggestionError extends Error {
  suggestion: Suggestion | null;
  constructor(suggestion: Suggestion | null) {
    super("stale suggestion");
    this.name = "StaleSuggestionError";
    this.suggestion = suggestion;
  }
}

/** The retention bounds, in days; 0 keeps raw events forever. */
export const RETENTION_MIN_DAYS = 30;
export const RETENTION_MAX_DAYS = 3650;
export const EXPLORATION_MAX_PCT = 50;

/** Checks an insights response. */
export function verifyInsights(_raw: unknown, _endpoint: string): Insights {
  // TODO(green): implement.
  throw new Error("verifyInsights: not implemented");
}

/** Checks a scorecards response. */
export function verifyScorecards(_raw: unknown, _endpoint: string): Scorecards {
  // TODO(green): implement.
  throw new Error("verifyScorecards: not implemented");
}

function notImplemented(): Promise<never> {
  return Promise.reject(new Error("analytics API: not implemented"));
}

export const analyticsApi = {
  insights: (_days: number, _project?: string): Promise<Insights> => notImplemented(),
  overview: (_days: number): Promise<Overview> => notImplemented(),
  scorecards: (_days: number, _track: Track, _owner?: string): Promise<Scorecards> => notImplemented(),
  tiers: (): Promise<TiersResponse> => notImplemented(),
  settings: (_project?: string): Promise<AnalyticsSettings> => notImplemented(),
  putSettings: (_settings: AnalyticsSettings, _project?: string): Promise<AnalyticsSettings> => notImplemented(),
  routePicks: (_limit: number, _project?: string): Promise<RoutePick[]> => notImplemented(),
  applySuggestion: (_key: string, _change: SuggestionChange, _project?: string): Promise<void> => notImplemented(),
  dismissSuggestion: (_key: string, _project?: string): Promise<void> => notImplemented(),
  retention: (): Promise<number> => notImplemented(),
  putRetention: (_days: number): Promise<number> => notImplemented(),
};
