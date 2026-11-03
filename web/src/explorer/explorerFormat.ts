import type { AgentItem } from "../api/explorer";
import { formatLocalizedTime } from "../i18n";
import { formatUSD } from "../usage/usageFormat";

type T = (key: string, values?: Record<string, string | number>) => string;

const BUILTIN_ROLES: ReadonlySet<string> = new Set(["planner", "qa", "tester", "executor", "reviewer", "researcher"]);

/** A role's name: a built-in's translated, a custom role's id. */
export function roleText(role: string, t: T): string {
  return BUILTIN_ROLES.has(role) ? t(`roles.name.${role}`) : role;
}

/** "Running", "Done", "Online"…; the raw state when it has no name. */
export function stateText(state: string, t: T): string {
  const key = `explorer.agent.state.${state}`;
  const text = t(key);
  return text === key ? state : text;
}

/** "$0.09", or "$0.09 (est.)" when it was estimated from tokens. */
export function costText(item: Pick<AgentItem, "costUsd" | "costEstimated">, t: T): string {
  const cost = formatUSD(item.costUsd);
  return item.costEstimated ? t("explorer.agent.estimate", { cost }) : cost;
}

/** "4m 12s" between two instants; "" when either is missing. */
export function durationText(startedAt: string, finishedAt: string, now = Date.now()): string {
  const start = Date.parse(startedAt);
  if (!Number.isFinite(start)) return "";
  const end = finishedAt ? Date.parse(finishedAt) : now;
  if (!Number.isFinite(end) || end < start) return "";
  const seconds = Math.round((end - start) / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return seconds % 60 ? `${minutes}m ${seconds % 60}s` : `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
}

/** "14:02": an event's time of day. */
export function clockText(at: string): string {
  return at ? formatLocalizedTime(at) : "";
}

/** "512 KB", "1.2 MB". */
export function sizeText(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 1024) return `${Math.max(0, Math.round(bytes || 0))} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** The last segment of a slash path. */
export function baseName(path: string): string {
  const parts = path.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/** Whether a path reads as Markdown. */
export function isMarkdown(path: string): boolean {
  return /\.(md|markdown)$/i.test(path);
}
