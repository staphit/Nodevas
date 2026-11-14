import { Suspense, useEffect, useRef } from "react";
import { lazyWithRetry } from "../lazyWithRetry";
import { useApp } from "../store";
import { useWorkspace } from "./workspaceStore";

// The panel (diff, file view, terminal) loads the first time one opens.
const RunWorkspaceDialog = lazyWithRetry(() => import("./RunWorkspaceDialog"));

/** Draws the open workspace, once for the whole app; drops the views it
 * held when the board switches project. */
export function RunWorkspaceHost() {
  const open = useWorkspace((s) => s.open);
  const close = useWorkspace((s) => s.close);
  const activeProject = useApp((s) => s.activeProject);
  const previousProject = useRef(activeProject);
  useEffect(() => {
    if (previousProject.current === activeProject) return;
    previousProject.current = activeProject;
    useWorkspace.getState().reset();
  }, [activeProject]);
  if (!open) return null;
  return (
    <Suspense fallback={null}>
      <RunWorkspaceDialog key={open.runId} runId={open.runId} tab={open.tab} onClose={close} />
    </Suspense>
  );
}
