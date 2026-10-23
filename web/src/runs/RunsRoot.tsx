import { useCallback, useEffect, useRef } from "react";
import type { Run } from "../api";
import { reportError, useApp } from "../store";
import { DispatchDialog } from "./DispatchDialog";
import { useRouting } from "./routingStore";
import { RunToasts } from "./RunToasts";
import { useRuns } from "./runStore";

export { openNodeRuns } from "./openRuns";

/**
 * Hosts the dispatch dialog for whichever node asked for it and the run
 * notification toasts, drops held runs when the board switches project, and
 * asks once per project for its recent runs so graph cards can badge their
 * latest one. The run view itself lives in the node's detail view (Drawer →
 * Runs).
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
      // "This project" in a rule, and the hub's answer to a GET, follow it.
      useRouting.getState().reset();
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

  return (
    <>
      <RunToasts />
      {dispatchNodeId && (
        <DispatchDialog nodeId={dispatchNodeId} onClose={closeDispatch} onCreated={onCreated} />
      )}
    </>
  );
}
