// A project's faces: Info is the board as it always was, Station is where
// the project's agents work together, Settings is the rest. Small on purpose:
// the top bar, the graph header and the command palette are in the main
// bundle and import it, while Station itself loads lazily.

import { useApp } from "../store";

export type ProjectMode = "info" | "station" | "settings";

/**
 * Shows Info, Station or Settings. Station is the agent workstation, so entering it
 * turns the workstation on: the "hidden until an agent appears" rule is for
 * Info, where a board used as a plain planner should stay plain.
 */
export function setProjectMode(mode: ProjectMode): void {
  const { updateUIPreference, preferences, closeDrawer } = useApp.getState();
  // In Station the node panel is an overlay for what Station itself opens.
  // One the board had open would cover the emergency stop and the side
  // column: it closes on the way in, its tab stays (as openAutopilot does).
  if (mode === "station" && preferences.projectMode !== "station") closeDrawer();
  updateUIPreference("projectMode", mode);
  if (mode === "station") updateUIPreference("agentWorkstation", true);
}
