import { req } from "./http";

/**
 * Token use and its cost: what runs reported and what the agent network's
 * own sessions reported through their hooks, summed per agent, model, day and
 * session. Costs are API-equivalent estimates from a price table (the agent
 * CLIs' own figure where they give one); a subscription login is not billed
 * per token, so they are a reference, not a bill.
 */

/** One sum. Every count is a token count except `runs` and `sessions`. */
export interface UsageBucket {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  /** reportedCostUsd + estimatedCostUsd. */
  costUsd: number;
  /** What the agent CLIs said themselves. */
  reportedCostUsd: number;
  /** Priced from the table. */
  estimatedCostUsd: number;
  /** Tokens of models the table has no price for: in the totals, not the cost. */
  unpricedTokens: number;
  runs: number;
  sessions: number;
}

export interface UsageGroup extends UsageBucket {
  key: string;
}

export interface UsageModelGroup extends UsageGroup {
  /** The agent kind the model ran under. */
  agent: string;
}

export type UsageSource = "session" | "run";

export interface UsageSession extends UsageBucket {
  id: string;
  source: UsageSource;
  name: string;
  actorName: string;
  agent: string;
  model: string;
  models: string[];
  nodeId: string;
  runId: string;
  firstAt: string;
  lastAt: string;
}

export interface UsageReport {
  days: number;
  since: string;
  currency: string;
  /** The date the built-in prices were checked. */
  pricesAsOf: string;
  totals: UsageBucket;
  byAgent: UsageGroup[];
  byModel: UsageModelGroup[];
  /** Ascending, one entry per day in the range. */
  byDay: UsageGroup[];
  /** Most recently active first. */
  sessions: UsageSession[];
  /** By the run's role (legacy runs by their kind; "session", "copilot"). */
  byRole: UsageGroup[];
  /** The top nodes by cost, across all their runs. */
  byNode: UsageGroup[];
}

export type PriceSource = "builtin" | "custom";

/** USD per million tokens. */
export interface ModelPrice {
  model: string;
  inputPerMTok: number;
  outputPerMTok: number;
  cacheReadPerMTok: number;
  cacheWritePerMTok: number;
  source: PriceSource;
}

export interface UsagePrices {
  prices: ModelPrice[];
  asOf: string;
}

/** The ranges the dialog offers, in days. */
export const USAGE_RANGES = [1, 7, 30, 90] as const;
export const MAX_USAGE_DAYS = 365;

function num(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function list<T>(value: unknown, item: (raw: Record<string, unknown>) => T): T[] {
  return Array.isArray(value)
    ? value.filter((v): v is Record<string, unknown> => !!v && typeof v === "object").map(item)
    : [];
}

/** A bucket with every field a number, whatever the server left out. */
export function normalizeBucket(raw: unknown): UsageBucket {
  const b = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const input = num(b.inputTokens);
  const output = num(b.outputTokens);
  const cacheRead = num(b.cacheReadTokens);
  const cacheWrite = num(b.cacheWriteTokens);
  const reported = num(b.reportedCostUsd);
  const estimated = num(b.estimatedCostUsd);
  return {
    inputTokens: input,
    outputTokens: output,
    cacheReadTokens: cacheRead,
    cacheWriteTokens: cacheWrite,
    totalTokens: num(b.totalTokens) || input + output + cacheRead + cacheWrite,
    costUsd: num(b.costUsd) || reported + estimated,
    reportedCostUsd: reported,
    estimatedCostUsd: estimated,
    unpricedTokens: num(b.unpricedTokens),
    runs: num(b.runs),
    sessions: num(b.sessions),
  };
}

export function normalizeUsage(raw: unknown): UsageReport {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  return {
    days: num(r.days),
    since: str(r.since),
    currency: str(r.currency) || "USD",
    pricesAsOf: str(r.pricesAsOf),
    totals: normalizeBucket(r.totals),
    byAgent: list(r.byAgent, (g) => ({ ...normalizeBucket(g), key: str(g.key) })),
    byModel: list(r.byModel, (g) => ({
      ...normalizeBucket(g),
      key: str(g.key),
      agent: str(g.agent),
    })),
    byDay: list(r.byDay, (g) => ({ ...normalizeBucket(g), key: str(g.key) })),
    sessions: list(r.sessions, (s) => ({
      ...normalizeBucket(s),
      id: str(s.id),
      source: s.source === "run" ? "run" : "session",
      name: str(s.name),
      actorName: str(s.actorName),
      agent: str(s.agent),
      model: str(s.model),
      models: Array.isArray(s.models) ? s.models.filter((m): m is string => typeof m === "string") : [],
      nodeId: str(s.nodeId),
      runId: str(s.runId),
      firstAt: str(s.firstAt),
      lastAt: str(s.lastAt),
    })),
    byRole: list(r.byRole, (g) => ({ ...normalizeBucket(g), key: str(g.key) })),
    byNode: list(r.byNode, (g) => ({ ...normalizeBucket(g), key: str(g.key) })),
  };
}

export function normalizePrices(raw: unknown): UsagePrices {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  return {
    asOf: str(r.asOf),
    prices: list(r.prices, (p) => ({
      model: str(p.model),
      inputPerMTok: num(p.inputPerMTok),
      outputPerMTok: num(p.outputPerMTok),
      cacheReadPerMTok: num(p.cacheReadPerMTok),
      cacheWritePerMTok: num(p.cacheWritePerMTok),
      source: p.source === "custom" ? "custom" : "builtin",
    })),
  };
}

function clampDays(days: number): number {
  if (!Number.isFinite(days)) return 30;
  return Math.min(MAX_USAGE_DAYS, Math.max(1, Math.round(days)));
}

export const usageApi = {
  getUsage: async (days: number): Promise<UsageReport> =>
    normalizeUsage(await req<unknown>(`/api/usage?days=${clampDays(days)}`)),
  getUsagePrices: async (): Promise<UsagePrices> =>
    normalizePrices(await req<unknown>("/api/usage/prices")),
  /** Replaces every custom price; built-in ones are not sent. */
  putUsagePrices: async (prices: Omit<ModelPrice, "source">[]): Promise<UsagePrices> =>
    normalizePrices(
      await req<unknown>("/api/usage/prices", {
        method: "PUT",
        body: JSON.stringify({
          prices: prices.map((p) => ({
            model: p.model.trim(),
            inputPerMTok: p.inputPerMTok,
            outputPerMTok: p.outputPerMTok,
            cacheReadPerMTok: p.cacheReadPerMTok,
            cacheWritePerMTok: p.cacheWritePerMTok,
          })),
        }),
      }),
    ),
};
