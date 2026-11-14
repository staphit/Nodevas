import { useWorkspace, type WorkspaceTab } from "./workspaceStore";

/**
 * Opens a run's agent workspace (its worktree, changes and terminal) over
 * whatever is on screen: the run views, the pixel war room, and any other
 * room that draws runs (a themed story room calls this, or dispatches
 * `{ kind: "run-workspace" }` through useGameActions). RunWorkspaceHost, in
 * App, draws it.
 */
export function openRunWorkspace(runId: string, tab?: WorkspaceTab): void {
  useWorkspace.getState().openWorkspace(runId, tab);
}
