import { reportError, useApp } from "../store";
import { useRuns } from "./runStore";

/** Opens a node's runs in its detail view, optionally with one run selected. */
export function openNodeRuns(nodeId: string, runId?: string): void {
  useRuns.getState().showRuns(nodeId, runId);
  void useApp.getState().openTab(nodeId).catch(reportError);
}
