// Everything waiting on the viewer in the current project, read once for
// both places that show it: Station's "Waiting on you" tiles and the war
// room's meeting room. Both read this list, so they cannot disagree.

import { useEffect, useMemo } from "react";

import { canAnswerAsk, useAgents } from "../agents/agentStore";
import type { Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useEdition, useLocalRunner } from "../runners/edition";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useGlobalAgents } from "./globalAgentsStore";
import { useStation } from "./stationStore";
import { waitingOnYou, type WaitingItem } from "./waiting";

/**
 * Single-user edition: the runs a result to apply may come from, the
 * freshest copy of each first: the run store's global runs, the global
 * agents list, then `stationRuns` (the project's). Undefined on a team hub,
 * which lists no results.
 */
export function useResultCandidates(stationRuns: Run[]): Run[] | undefined {
  const single = useEdition() === "single";
  const globalById = useRuns((s) => s.globalById);
  const globalRuns = useGlobalAgents((s) => s.runs);
  const globalLoaded = useGlobalAgents((s) => s.loaded);
  // The global runs are loaded while Station is on screen; a person who
  // reopens the app on the board would otherwise miss a result that finished
  // meanwhile (most of a Simple user's results are global runs). Loaded once
  // here; a failed load counts as loaded and is not retried in a loop.
  useEffect(() => {
    if (single && !globalLoaded) void useGlobalAgents.getState().load();
  }, [single, globalLoaded]);
  return useMemo(
    () => (single ? [...Object.values(globalById), ...globalRuns, ...stationRuns] : undefined),
    [single, globalById, globalRuns, stationRuns],
  );
}

export function useWaitingItems(): WaitingItem[] {
  const runs = useStation((s) => s.runs);
  const active = useRuns((s) => s.activeRuns);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const reviews = useAutopilot((s) => s.waiting);
  const exceeded = useBudget((s) => s.status?.exceeded);
  const builtInRunnerId = useLocalRunner()?.id;
  const results = useResultCandidates(runs);
  return useMemo(
    () =>
      waitingOnYou({
        runs,
        active,
        asks,
        held,
        reviews,
        budgetExceeded: exceeded,
        canAnswer: (ask) => canAnswerAsk(ask, viewer),
        now: Date.now(),
        builtInRunnerId,
        results,
      }),
    [runs, active, asks, held, reviews, exceeded, viewer, builtInRunnerId, results],
  );
}
