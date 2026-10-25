import type { Run, RunUsage } from "../api";
import { languageLocale } from "../i18n";

/** "12,345" in the reader's locale. */
export function formatCount(value: number): string {
  return new Intl.NumberFormat(languageLocale()).format(value);
}

/** "12.3k" — short enough for a list row. */
export function formatCompact(value: number): string {
  return new Intl.NumberFormat(languageLocale(), {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
}

export function totalTokens(usage: RunUsage | undefined): number {
  if (!usage) return 0;
  return (usage.inputTokens ?? 0) + (usage.outputTokens ?? 0);
}

/** "$0.042"; small amounts keep enough digits to not read as zero. */
export function formatCost(costUsd: number | undefined): string {
  if (!costUsd || !Number.isFinite(costUsd)) return "";
  const digits = costUsd < 0.01 ? 4 : costUsd < 1 ? 3 : 2;
  return `$${costUsd.toFixed(digits)}`;
}

/** Pretty JSON when the input is JSON, the text as given otherwise. */
export function prettyInput(input: string): string {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
}

/** One line, cut to `max` characters, for a collapsed summary. */
export function oneLine(text: string, max = 96): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

/** Whether a run reported a commit or line counts worth a summary. */
export function runHasChanges(
  run: Pick<Run, "commit" | "filesChanged" | "additions" | "deletions">,
): boolean {
  return (
    Boolean(run.commit) ||
    (run.filesChanged ?? 0) > 0 ||
    (run.additions ?? 0) > 0 ||
    (run.deletions ?? 0) > 0
  );
}

export type RunBadgeKind =
  | "waiting"
  | "running"
  | "ci-failure"
  | "pr-merged"
  | "pr-open"
  | "succeeded"
  | "failed";

/** What a graph card says about its node's latest run, or null when nothing
 * is worth a badge (cancelled, declined, unknown). The run's own progress wins
 * while it is live; after that its pull request does, a failing CI first. */
export function runBadgeKind(run: Run): RunBadgeKind | null {
  if (run.status === "running") return "running";
  if (run.status === "queued" || run.status === "offered") return "waiting";
  if (run.prUrl && run.prState === "open" && run.ciStatus === "failure") return "ci-failure";
  if (run.prUrl && run.prState === "merged") return "pr-merged";
  if (run.prUrl && run.prState === "open") return "pr-open";
  if (run.status === "succeeded") return "succeeded";
  if (run.status === "failed") return "failed";
  return null;
}

/** Only http(s) links leave the page: a PR URL is the runner's word. */
export function safeExternalUrl(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" || parsed.protocol === "http:" ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

/** Splits prose on blank lines; a single newline stays inside a paragraph. */
export function paragraphs(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((part) => part.trim())
    .filter(Boolean);
}
