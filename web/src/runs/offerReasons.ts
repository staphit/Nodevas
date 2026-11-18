// Why a runner asks before it starts a run, and why it did not start one, in
// the reader's words, with what would change it. The codes are the runner's
// (runnerwire.AskReason, the decline codes); the single-user edition's app
// card and a team hub's "waiting on <PC>" line both read them from here.

import type { AskReason, Run } from "../api";
import { agentName } from "../game/labels";
import { runnerDisplayName } from "../runners/runnerName";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** Where a reason or a refusal is fixed: what an agent may do or when to ask
 * (This computer's settings), or a repo to add. */
export type OfferFix = "permission" | "askEveryRun" | "addRepo" | "local";

const ORIGINS = new Set(["autopilot", "pipeline", "handoff"]);

/** How an automatic run started ("by Autopilot"), or "" for a run a person
 * started or whose start is not known. */
export function originText(t: Translate, origin: string | undefined): string {
  return origin && ORIGINS.has(origin) ? t(`offerAsk.origin.${origin}`) : "";
}

/** The machine a run waits on: the ask's own name for it, else the runner's. */
export function askMachine(t: Translate, run: Pick<Run, "runnerName" | "runnerId" | "ask">): string {
  return run.ask?.machine || runnerDisplayName(t, run.runnerName) || run.runnerId;
}

/** What a not_on_list reason misses, as words: the repo by name, the agent
 * by name, or "this kind of run". */
function missingText(t: Translate, reason: AskReason, run: Pick<Run, "repoName" | "repoId" | "agent">): string {
  switch (reason.missing) {
    case "repo":
      return run.repoName || t("offerAsk.missing.repo");
    case "agent":
      return agentName(reason.agent || run.agent);
    default:
      return t("offerAsk.missing.mode");
  }
}

const REASONS = new Set(["shell_agent", "ask_every_run", "not_on_list", "needs_step_up", "verify_tests"]);

/** One reason in the reader's words; "" for a code this app does not know
 * (a newer runner's), which the card then leaves out. */
export function askReasonText(
  t: Translate,
  reason: AskReason,
  run: Pick<Run, "repoName" | "repoId" | "agent" | "origin" | "runnerName" | "runnerId" | "ask">,
): string {
  if (!REASONS.has(reason.code)) return "";
  const agent = agentName(reason.agent || run.agent);
  const machine = askMachine(t, run);
  if (reason.code === "shell_agent") {
    const origin = originText(t, run.origin);
    return origin
      ? t("offerAsk.reason.shell_agent", { agent, origin })
      : t("offerAsk.reason.shell_agentNoOrigin", { agent });
  }
  if (reason.code === "not_on_list") {
    return t("offerAsk.reason.not_on_list", { missing: missingText(t, reason, run), machine });
  }
  return t(`offerAsk.reason.${reason.code}`, { agent, machine });
}

/** Where the reason can be changed in the single-user edition, or null. */
export function askReasonFix(reason: AskReason): OfferFix | null {
  if (reason.code === "shell_agent") return "permission";
  if (reason.code === "ask_every_run") return "askEveryRun";
  return null;
}

/** The agent kind a shell_agent reason names, or "": the card's "don't ask
 * again" box is about it. */
export function shellAgentOf(reasons: readonly AskReason[] | null | undefined, fallback = ""): string {
  const reason = reasons?.find((item) => item.code === "shell_agent");
  return reason ? reason.agent || fallback : "";
}

const DECLINES = new Set([
  "declined_in_app",
  "declined_on_runner",
  "timed_out",
  "no_one_there",
  "repo_not_added",
  "agent_missing",
  "agent_signed_out",
  "interactive_off",
  "global_off",
  "busy",
  "other",
]);

/**
 * Why a declined run did not start, in plain words; "" when the run carries
 * no code this app knows (an older hub's), so the caller keeps its generic
 * line. In the single-user edition the machine is "this computer", and
 * no_one_there (nobody at a tray) cannot happen there.
 */
export function declineText(
  t: Translate,
  run: Pick<Run, "declineCode" | "agent" | "runnerName" | "runnerId" | "ask">,
  single: boolean,
): string {
  const code = run.declineCode ?? "";
  if (!DECLINES.has(code)) return "";
  const machine = single ? t("local.machine") : askMachine(t, run);
  return t(`runs.decline.${code}`, { machine, agent: agentName(run.agent) });
}

/** What fixes a decline in the single-user edition, or null. */
export function declineFix(code: string | undefined): OfferFix | null {
  if (code === "repo_not_added") return "addRepo";
  if (code === "agent_missing" || code === "agent_signed_out") return "local";
  return null;
}
