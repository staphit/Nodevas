/**
 * The first-run questions' gate (single-user edition): whether someone looks
 * new enough to be asked, and whether the questions are holding the screen.
 *
 * Small on purpose: StartScreen and the tour read the hold, and Settings sends
 * the event that asks again, without pulling the questions in.
 */

import { create } from "zustand";

/** Window event that asks the questions again (Settings / General). */
export const OPEN_FIRST_RUN_EVENT = "nodevas-open-first-run";

/** What says whether this person already used the app. */
export interface FirstRunInputs {
  /** One project with nothing in it (StartScreen's useFreshWorkspace). */
  freshWorkspace: boolean;
  startScreenDismissed: boolean;
  /** The tour invitation: "pending" until answered. */
  tourPrompt: string;
  tourChaptersDone: readonly string[];
  agentWorkstation: boolean;
  graphConnectTipDismissed: boolean;
  /** A runner, a run or a connected agent is already here. */
  workstationInUse: boolean;
  experience: string;
}

/**
 * Whether to ask: a fresh workspace and no sign at all of someone who used the
 * app before. Any one sign is enough not to; asking an existing person would
 * only get in their way.
 */
export function looksNew(inputs: FirstRunInputs): boolean {
  return (
    inputs.freshWorkspace &&
    !inputs.startScreenDismissed &&
    inputs.tourPrompt === "pending" &&
    inputs.tourChaptersDone.length === 0 &&
    !inputs.agentWorkstation &&
    !inputs.graphConnectTipDismissed &&
    !inputs.workstationInUse &&
    inputs.experience === "full"
  );
}

/** True while the questions are deciding or on screen: the start screen and
 * the tour's invitation wait, so a first launch shows one dialog at a time. */
const useFirstRunGate = create<{ holding: boolean }>(() => ({ holding: false }));

export function useFirstRunHolding(): boolean {
  return useFirstRunGate((s) => s.holding);
}

/** The hold now, outside React (a timer about to open another dialog). */
export function firstRunHolding(): boolean {
  return useFirstRunGate.getState().holding;
}

export function setFirstRunHolding(holding: boolean): void {
  if (useFirstRunGate.getState().holding !== holding) useFirstRunGate.setState({ holding });
}
