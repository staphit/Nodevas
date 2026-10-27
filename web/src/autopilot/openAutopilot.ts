import { setProjectMode } from "../station/projectMode";
import { useStation } from "../station/stationStore";
import { useApp } from "../store";

/**
 * Opens a goal's autopilot in Station: Autopilot directs agents, so it lives
 * where the agents are, with the goal and its tasks still the Info graph's
 * nodes. The node panel's banner, its menu and a task waiting for a person
 * all come here.
 */
export function openAutopilot(nodeId: string): void {
  useStation.getState().focusGoal(nodeId, useApp.getState().activeProject);
  // The node's panel would cover the goal; its tab stays open.
  useApp.getState().closeDrawer();
  setProjectMode("station");
}
