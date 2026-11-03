import type { AgentModel } from "../api/runs";
import type { PickReasonCode, Suggestion, SuggestionChange } from "../api/analytics";
import { formatUSD } from "../usage/usageFormat";

type T = (key: string, values?: Record<string, string | number>) => string;

const DASH = "—";

/** A rate as a percentage; an em dash when there is none. */
export function formatRate(value: number | null): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return DASH;
  return `${Math.round(value * 100)}%`;
}

/** A plain number to two decimals; an em dash when there is none. */
export function formatRounds(value: number | null): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return DASH;
  return value.toFixed(2);
}

/** A cost in USD, "(est.)" when any part of it is an estimate. */
export function formatCost(value: number | null, estimated: boolean, t: T): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return DASH;
  const cost = formatUSD(value);
  return estimated ? t("explorer.agent.estimate", { cost }) : cost;
}

/** "4m 12s", "38s", "1h 5m"; an em dash when there is none. */
export function formatSeconds(value: number | null): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return DASH;
  const total = Math.round(value);
  if (total < 60) return `${total}s`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return total % 60 ? `${minutes}m ${total % 60}s` : `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
}

/** "codex/gpt-5.5", or the agent alone when it runs its default model. */
export function agentModelText(target: AgentModel | undefined): string {
  if (!target) return "";
  return target.model ? `${target.agent}/${target.model}` : target.agent;
}

/** A change, for the confirm dialog. */
export function changeText(change: SuggestionChange, t: T): string {
  switch (change.type) {
    case "routing_rule_target":
      return t("analytics.change.routing_rule_target", {
        rule: change.ruleId ?? "",
        from: agentModelText(change.from),
        to: agentModelText(change.to),
      });
    case "auto_routing":
      return t("analytics.change.auto_routing", { project: change.project ?? "" });
    case "role_budget":
      return t("analytics.change.role_budget", {
        role: change.role ?? "",
        cost: formatUSD(change.dailyCostUsd ?? 0),
      });
    default:
      return "";
  }
}

/** A suggestion's sentence. Its params are the hub's; from/to come from the
 * change when the params leave them out. */
export function suggestionText(suggestion: Suggestion, t: T): string {
  const values: Record<string, string | number> = { ...suggestion.params };
  const change = suggestion.change;
  if (change?.from && values.from === undefined) values.from = agentModelText(change.from);
  if (change?.to && values.to === undefined) values.to = agentModelText(change.to);
  if (change?.role && values.role === undefined) values.role = change.role;
  if (change?.dailyCostUsd !== undefined && values.cost === undefined) values.cost = formatUSD(change.dailyCostUsd);
  const key = `analytics.suggest.${suggestion.kind}`;
  const text = t(key, values);
  return text === key ? suggestion.key : text;
}

/** A pick's reason. */
export function pickReasonText(code: PickReasonCode | string, values: Record<string, string | number>, t: T): string {
  const key = `analytics.pick.reason.${code}`;
  const text = t(key, values);
  return text === key ? String(code) : text;
}
