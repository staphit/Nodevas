// Everything waiting on the viewer in the current project, read once for
// both places that show it: Station's "Waiting on you" tiles and the war
// room's meeting room. Both read this list, so they cannot disagree.

import { useMemo } from "react";

import { canManageSession, useAgents } from "../agents/agentStore";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useStation } from "./stationStore";
import { waitingOnYou, type WaitingItem } from "./waiting";

export function useWaitingItems(): WaitingItem[] {
  const runs = useStation((s) => s.runs);
  const active = useRuns((s) => s.activeRuns);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const reviews = useAutopilot((s) => s.waiting);
  const exceeded = useBudget((s) => s.status?.exceeded);
  return useMemo(
    () =>
      waitingOnYou({
        runs,
        active,
        asks,
        held,
        reviews,
        budgetExceeded: exceeded,
        canAnswer: (ask) => canManageSession(ask, viewer),
        now: Date.now(),
      }),
    [runs, active, asks, held, reviews, exceeded, viewer],
  );
}
