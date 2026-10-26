import { useEffect } from "react";
import { runFinal, type Run } from "../api";
import { useRuns } from "../runs/runStore";

/**
 * The goal's plan run that has not ended, newest first: a breakdown, or the
 * replan of a revision (`run.replan`) the voice copilot started.
 */
export function activePlanRun(runs: Run[] | undefined): Run | undefined {
  return runs?.find((run) => run.kind === "plan" && !runFinal(run.status));
}

/** The goal's plan run in progress; loads the goal's runs once if nothing
 * has yet. runs-changed keeps them current from then on. */
export function usePlanningRun(goalNodeId: string): Run | undefined {
  const runs = useRuns((s) => s.runsByNode[goalNodeId]);
  const loaded = runs !== undefined;
  useEffect(() => {
    if (!loaded) void useRuns.getState().loadRuns(goalNodeId);
  }, [goalNodeId, loaded]);
  return activePlanRun(runs);
}
