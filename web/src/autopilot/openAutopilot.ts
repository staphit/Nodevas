import { reportError, useApp } from "../store";
import { useAutopilot } from "./autopilotStore";

/** Opens a node's detail view on its Autopilot tab. */
export function openAutopilot(nodeId: string): void {
  useAutopilot.getState().showAutopilot(nodeId);
  void useApp.getState().openTab(nodeId).catch(reportError);
}
