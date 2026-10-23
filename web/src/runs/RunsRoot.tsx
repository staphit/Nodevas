import { useCallback, useEffect, useRef } from "react";
import type { Run } from "../api";
import { reportError, useApp } from "../store";
import { DispatchDialog } from "./DispatchDialog";
import { useRuns } from "./runStore";

/**
 * Hosts the dispatch dialog for whichever node asked for it, drops held runs
 * when the board switches project, and asks once per project for its recent
 * runs so graph cards can badge their latest one. The run view itself lives in the
 * node's detail view (Drawer → Runs).
 */
export function RunsRoot() {
  const dispatchNodeId = useRuns((s) => s.dispatchNodeId);
  const closeDispatch = useRuns((s) => s.closeDispatch);
  const activeProject = useApp((s) => s.activeProject);
  const previousProject = useRef(activeProject);

  useEffect(() => {
    if (previousProject.current !== activeProject) {
      previousProject.current = activeProject;
      useRuns.getState().reset();
    }
    // One request per project feeds every graph card's run badge; after it,
    // runs-changed pushes keep the badges current node by node.
    if (activeProject) void useRuns.getState().loadProjectRuns();
  }, [activeProject]);

  const onCreated = useCallback(
    (run: Run) => {
      closeDispatch();
      useRuns.getState().showRuns(run.nodeId, run.id);
      void useApp.getState().openTab(run.nodeId).catch(reportError);
    },
    [closeDispatch],
  );

  if (!dispatchNodeId) return null;
  return (
    <DispatchDialog nodeId={dispatchNodeId} onClose={closeDispatch} onCreated={onCreated} />
  );
}

/** Opens a node's runs in its detail view. */
export function openNodeRuns(nodeId: string): void {
  useRuns.getState().showRuns(nodeId);
  void useApp.getState().openTab(nodeId).catch(reportError);
}
