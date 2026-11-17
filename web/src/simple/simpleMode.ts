/**
 * Simple mode: the preset for someone who does not write code.
 *
 * One account preference, `experience`, says how a person uses Nodevas
 * ("full" or "simple"), and it only has an effect in the single-user
 * edition: a team hub never reads it. Code asks whether the preset is on in
 * exactly one place, useSimpleMode below. What the preset does (the gates
 * lifted through the audited override, the built-in runner's PRs held off,
 * the plain instruction on each run, the describe box first) is done by the
 * web through owner-only APIs that already exist; the hub never reads the
 * preference.
 */

import { useEdition } from "../runners/edition";
import { useApp, usePreference } from "../store";

export const EXPERIENCES = ["full", "simple"] as const;
export type Experience = (typeof EXPERIENCES)[number];

/** The reason a Simple run that a gate would block starts with: the
 * existing override, recorded on the run and in the audit log. Fixed
 * English text, so an audit search finds every one. */
export const SIMPLE_OVERRIDE_REASON = "Simple mode: started without checks";

/** Whether the Simple preset is on: the person chose it, and the app is the
 * single-user edition. */
export function useSimpleMode(): boolean {
  const pref = usePreference("experience");
  const edition = useEdition();
  return pref === "simple" && edition === "single";
}

/** Saves how this person uses Nodevas (follows their account). */
export function setExperience(value: Experience): void {
  useApp.getState().updateUIPreference("experience", value);
}
