import { useGlobalAgents } from "./globalAgentsStore";
import { setProjectMode } from "./projectMode";
import { useStation } from "./stationStore";

/**
 * Opens Station on its Global agents tab, with `runId` (when given)
 * highlighted in the list: the war room's second office, a toast, the
 * command palette all come here. Global agents belong to no project, so the
 * board's project stays as it is.
 */
export function openGlobalAgents(runId?: string): void {
  setProjectMode("station");
  useStation.getState().setTab("global");
  if (runId) useGlobalAgents.getState().setFocus(runId);
}
