import type { AutoAcceptPolicy, StepUpStatus } from "../api";
import { stepUpActive } from "../stepup/stepUpStore";

/** runnerwire.ModeInteractive; "" is headless. */
export const MODE_INTERACTIVE = "interactive";

/** Why a job would wait for someone at the runner. */
export type ManualReason = "off" | "repo" | "agent" | "mode" | "stepUpUnavailable";

/**
 * What a runner will do with a dispatch, from its auto-accept policy and the
 * dispatcher's step-up:
 * - auto: it starts on its own. `until` is the step-up's end when the policy
 *   needs one; `stepUp` says whether the policy needs one at all.
 * - verify: it would start on its own once the dispatcher steps up.
 * - manual: someone at the runner must accept (it declines when nobody does).
 * - pending: the step-up status is still being fetched.
 */
export type AutoAcceptOutcome =
  | { kind: "auto"; stepUp: "active" | "notRequired" | "notNeeded"; until?: string }
  | { kind: "verify" }
  | { kind: "manual"; reason: ManualReason }
  | { kind: "pending" };

export interface AutoAcceptJob {
  repoId: string;
  agent: string;
  /** "" (headless) or "interactive". */
  mode?: string;
}

/** The policy's rule for modes: empty means headless only; "headless" is
 * read as "" for a runner that spells it out. */
export function modeAllowed(policy: AutoAcceptPolicy, mode: string): boolean {
  const wanted = mode === "headless" ? "" : mode;
  const modes = (policy.modes ?? []).map((item) => (item === "headless" ? "" : item));
  return modes.length === 0 ? wanted === "" : modes.includes(wanted);
}

/**
 * The runner's own check (it enforces this; the hub only shows it), plus the
 * hub's step-up. `stepUp` is undefined while being fetched and null when the
 * hub could not say. A hub that does not require step-up (loopback, no
 * accounts) vouches for every dispatch, so the policy's step-up is met.
 */
export function autoAcceptOutcome(
  policy: AutoAcceptPolicy | null | undefined,
  stepUp: StepUpStatus | null | undefined,
  job: AutoAcceptJob,
  now = Date.now(),
): AutoAcceptOutcome {
  if (!policy?.enabled) return { kind: "manual", reason: "off" };
  if (!job.repoId || !(policy.repos ?? []).includes(job.repoId)) {
    return { kind: "manual", reason: "repo" };
  }
  if (!job.agent || !(policy.agents ?? []).includes(job.agent)) {
    return { kind: "manual", reason: "agent" };
  }
  if (!modeAllowed(policy, job.mode ?? "")) return { kind: "manual", reason: "mode" };
  if (!policy.requireStepUp) return { kind: "auto", stepUp: "notRequired" };
  if (stepUp === undefined) return { kind: "pending" };
  if (stepUp === null) return { kind: "manual", reason: "stepUpUnavailable" };
  if (!stepUp.required) return { kind: "auto", stepUp: "notNeeded" };
  if (stepUpActive(stepUp, now)) return { kind: "auto", stepUp: "active", until: stepUp.until };
  return { kind: "verify" };
}
