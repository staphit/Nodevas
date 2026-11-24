import { GlobalRunError } from "../../api";

/** A follow-up refusal code (internal/runs/followup.go) → its sentence. */
export const FOLLOW_UP_ERROR_KEYS: Readonly<Record<string, string>> = {
  CONTINUED: "runDialog.err.continued",
  IN_USE: "runDialog.err.inUse",
  WORK_GONE: "runDialog.err.gone",
  RUNNER_OUTDATED: "runDialog.err.outdated",
  RUN_LIVE: "runDialog.err.live",
  PR_ENDED: "runDialog.err.prEnded",
  BRANCH_MOVED: "runDialog.err.branchMoved",
  PR_MOVED: "runDialog.err.prMoved",
  MERGE_PENDING: "runDialog.err.mergePending",
  TURN_RUNNING: "runDialog.err.turnRunning",
};

/** The same refusals in Simple's plain words (runDialog.simple.*, checked by
 * i18nPlainWords.test.ts): Simple never shows a developer's sentence. */
export const SIMPLE_FOLLOW_UP_ERROR_KEYS: Readonly<Record<string, string>> = {
  CONTINUED: "runDialog.simple.err.continued",
  IN_USE: "runDialog.simple.err.inUse",
  WORK_GONE: "runDialog.simple.err.gone",
  RUNNER_OUTDATED: "runDialog.simple.err.outdated",
  RUN_LIVE: "runDialog.simple.err.live",
  PR_ENDED: "runDialog.simple.err.prEnded",
  BRANCH_MOVED: "runDialog.simple.err.branchMoved",
  PR_MOVED: "runDialog.simple.err.prMoved",
  MERGE_PENDING: "runDialog.simple.err.mergePending",
  TURN_RUNNING: "runDialog.simple.err.turnRunning",
};

/** The catalog key for a follow-up refusal code, or undefined when unknown. */
export function followUpErrorKey(code: string, simple: boolean): string | undefined {
  return (simple ? SIMPLE_FOLLOW_UP_ERROR_KEYS : FOLLOW_UP_ERROR_KEYS)[code];
}

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** A refused follow-up, End or new start in the reader's words. */
export function mapFollowUpError(error: unknown, t: Translate, simple: boolean): string {
  if (error instanceof GlobalRunError) {
    const key = followUpErrorKey(error.code, simple);
    if (key) return t(key);
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

/** A group member's skip code the hub returns beyond the follow-up refusals
 * (internal/runs/groupfollowup.go groupFollowUpSkipCode) → its plain Simple
 * sentence (checked by i18nPlainWords.test.ts). */
export const SIMPLE_GROUP_SKIP_KEYS: Readonly<Record<string, string>> = {
  BUDGET_EXCEEDED: "globalAgents.simple.skipBudget",
  FAILED_PRECONDITION: "globalAgents.simple.skipNotReady",
  NOT_FOUND: "globalAgents.simple.skipGone",
  INVALID_ARGUMENT: "globalAgents.simple.skipCannot",
};

/** Simple's sentence for a skip code it has no sentence for yet. */
export const SIMPLE_GROUP_SKIP_OTHER = "globalAgents.simple.skipOther";

/** Why the hub skipped a group member, in the reader's words. Simple never
 * shows a raw code; the full view names an unknown one. */
export function groupSkipReason(code: string, t: Translate, simple: boolean): string {
  const key = followUpErrorKey(code, simple);
  if (key) return t(key);
  if (simple) return t(SIMPLE_GROUP_SKIP_KEYS[code] ?? SIMPLE_GROUP_SKIP_OTHER);
  if (code === "BUDGET_EXCEEDED") return t("globalAgents.group.skipBudget");
  return t("globalAgents.group.skipOther", { code });
}
