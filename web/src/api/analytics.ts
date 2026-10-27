import { AuthError, RequestError, req, send } from "./http";
import type { AgentModel } from "./runs";
import { ApiShapeError } from "./verify";

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

function shape(endpoint: string, field: string, expected: string, value: unknown): never {
  const actual = value === null ? "null" : value === undefined ? "缺少" : Array.isArray(value) ? "array" : typeof value;
  throw new ApiShapeError(endpoint, field, expected, actual);
}

function obj(endpoint: string, value: unknown, field: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) shape(endpoint, field, "object", value);
  return value as Record<string, unknown>;
}

function num(endpoint: string, value: unknown, field: string): number {
  if (typeof value !== "number" || Number.isNaN(value)) shape(endpoint, field, "number", value);
  return value as number;
}

/** Go marshals a nil slice as null: that is an empty list. */
function list(endpoint: string, value: unknown, field: string): unknown[] {
  if (value === null || value === undefined) return [];
  if (!Array.isArray(value)) shape(endpoint, field, "array", value);
  return value as unknown[];
}

/** Checks an insights response. */
export function verifyInsights(raw: unknown, endpoint: string): Insights {
  const body = obj(endpoint, raw, "insights");
  num(endpoint, body.days, "days");
  const totals = obj(endpoint, body.totals, "totals");
  num(endpoint, totals.costUsd, "totals.costUsd");
  num(endpoint, totals.delivered, "totals.delivered");
  const waste = obj(endpoint, body.waste, "waste");
  for (const field of ["costUsd", "failedCostUsd", "retriedCostUsd", "abandonedCostUsd"]) {
    num(endpoint, waste[field], `waste.${field}`);
  }
  const hotspots = list(endpoint, waste.hotspots, "waste.hotspots");
  const buckets = (field: string) => list(endpoint, body[field], field) as CostBucket[];
  return {
    ...(body as unknown as Insights),
    byRole: buckets("byRole"),
    byAgent: buckets("byAgent"),
    byModel: buckets("byModel"),
    byNodeType: buckets("byNodeType"),
    waste: { ...(waste as unknown as Waste), hotspots: hotspots as WasteHotspot[] },
    suggestions: list(endpoint, body.suggestions, "suggestions") as Suggestion[],
  };
}

/** Checks a scorecards response. */
export function verifyScorecards(raw: unknown, endpoint: string): Scorecards {
  const body = obj(endpoint, raw, "scorecards");
  num(endpoint, body.days, "days");
  const rows = list(endpoint, body.rows, "rows");
  rows.forEach((row, i) => obj(endpoint, row, `rows[${i}]`));
  return { ...(body as unknown as Scorecards), rows: rows as ScorecardRow[] };
}

/** "?days=30&project=p": the project parameter only when one is named. */
function query(params: Record<string, string | number | undefined>): string {
  const parts: string[] = [];
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === "") continue;
    parts.push(`${key}=${encodeURIComponent(String(value))}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

const json = (body: unknown): RequestInit => ({ method: "POST", body: JSON.stringify(body) });

async function applySuggestion(key: string, change: SuggestionChange, project?: string): Promise<void> {
  const url = `/api/analytics/suggestions/apply${query({ project })}`;
  const res = await send(url, json({ key, change }));
  if (res.ok) return;
  const body = (await res.json().catch(() => ({}))) as { error?: unknown; code?: unknown; suggestion?: unknown };
  if (res.status === 409 && body.code === "STALE_SUGGESTION") {
    const fresh = body.suggestion;
    throw new StaleSuggestionError(fresh && typeof fresh === "object" ? (fresh as Suggestion) : null);
  }
  const message = typeof body.error === "string" && body.error ? body.error : `${res.status}`;
  if (res.status === 403) throw new AuthError(403, message);
  throw new RequestError(res.status, message, typeof body.code === "string" ? body.code : "");
}

export const analyticsApi = {
  insights: (days: number, project?: string): Promise<Insights> =>
    req(`/api/analytics/insights${query({ days, project })}`, undefined, verifyInsights),
  overview: (days: number): Promise<Overview> =>
    req<Overview>(`/api/analytics/overview${query({ days })}`).then((o) => ({ byProject: o?.byProject ?? [] })),
  scorecards: (days: number, track: Track, owner?: string): Promise<Scorecards> =>
    req(`/api/analytics/scorecards${query({ days, track, owner })}`, undefined, verifyScorecards),
  tiers: (): Promise<TiersResponse> =>
    req<TiersResponse>("/api/analytics/tiers").then((t) => ({ ...t, required: t.required ?? {}, rows: t.rows ?? [] })),
  settings: (project?: string): Promise<AnalyticsSettings> =>
    req<AnalyticsSettings>(`/api/analytics/settings${query({ project })}`).then((s) => ({
      ...s,
      requiredTiers: s.requiredTiers ?? {},
    })),
  putSettings: (settings: AnalyticsSettings, project?: string): Promise<AnalyticsSettings> =>
    req<AnalyticsSettings>(`/api/analytics/settings${query({ project })}`, {
      method: "PUT",
      body: JSON.stringify({
        autoRouting: settings.autoRouting,
        explorationPct: settings.explorationPct,
        requiredTiers: settings.requiredTiers,
      }),
    }),
  routePicks: (limit: number, project?: string): Promise<RoutePick[]> =>
    req<{ picks: RoutePick[] | null }>(`/api/analytics/route-picks${query({ limit, project })}`).then(
      (r) => r?.picks ?? [],
    ),
  applySuggestion,
  dismissSuggestion: (key: string, project?: string): Promise<void> =>
    req<void>(`/api/analytics/suggestions/dismiss${query({ project })}`, json({ key })).then(() => undefined),
  retention: (): Promise<number> => req<{ days: number }>("/api/analytics/retention").then((r) => r.days),
  putRetention: (days: number): Promise<number> =>
    req<{ days: number }>("/api/analytics/retention", { method: "PUT", body: JSON.stringify({ days }) }).then(
      (r) => r?.days ?? days,
    ),
};
