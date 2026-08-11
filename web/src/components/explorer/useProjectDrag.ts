/**
 * Dragging projects between places [B-06].
 *
 * A dragged project can land on another project (re-parenting inside the open
 * workspace) or on a different workspace root (moving the directory out of this
 * workspace); both are directory moves, so both invalidate the project list.
 * Landing *between* two rows is the third, cheaper case: nothing on disk moves,
 * only the workspace's manual order is rewritten.
 */

import { useState } from "react";
import { api } from "../../api";
import type { ProjectEntry } from "../../state/types";
import { reportError, useApp } from "../../store";
import { reorderProjectNames, sortProjectTree, type ProjectSort } from "./sortProjects";

/** Where a between-rows drop would land: above or below one specific row. */
export type ProjectDropEdge = { name: string; before: boolean };

export function useProjectDrag({
  workspace,
  switchingWorkspace,
  setSwitchingWorkspace,
  expandedProjects,
  persistExpandedProjects,
  setProjectTransferNotice,
  projects,
  projectSort,
  projectOrder,
  setProjectSort,
}: {
  workspace: string;
  switchingWorkspace: string | null;
  setSwitchingWorkspace: (path: string | null) => void;
  expandedProjects: Set<string>;
  persistExpandedProjects: (next: Set<string>) => void;
  setProjectTransferNotice: (notice: string | null) => void;
  projects: ProjectEntry[];
  projectSort: ProjectSort;
  projectOrder: string[];
  setProjectSort: (sort: ProjectSort) => void;
}) {
  const refreshProjects = useApp((state) => state.refreshProjects);
  const loadAll = useApp((state) => state.loadAll);
  const moveProjectToWorkspace = useApp(
    (state) => state.moveProjectToWorkspace,
  );
  const saveProjectOrder = useApp((state) => state.saveProjectOrder);
  const [projectDropTarget, setProjectDropTarget] = useState<string | null>(null);
  const [projectDropEdge, setProjectDropEdge] = useState<ProjectDropEdge | null>(
    null,
  );

  /**
   * Reordering only ever rewrites one level, but the whole flattened list is
   * sent: the stored order has to describe every level, or the levels nobody
   * touched would fall back to name order the next time manual sort is read.
   */
  const reorderProject = async (
    moved: string,
    target: string,
    before: boolean,
  ) => {
    setProjectDropTarget(null);
    setProjectDropEdge(null);
    if (!moved || moved === target) return;
    const movedEntry = projects.find((project) => project.name === moved);
    const targetEntry = projects.find((project) => project.name === target);
    if (!movedEntry || !targetEntry) return;
    if ((movedEntry.parent ?? "") !== (targetEntry.parent ?? "")) {
      setProjectTransferNotice(
        "只能在同一層之間調整順序；要換層級請把專案拖到另一個專案上。",
      );
      return;
    }
    const ordered = sortProjectTree(projects, projectSort, projectOrder);
    const next = reorderProjectNames(ordered, moved, target, before);
    // Switching sort here is lossless rather than destructive: the order being
    // stored is exactly what the previous sort was already showing, so the tree
    // does not jump, and the sort control right above it undoes the switch.
    if (projectSort !== "manual") setProjectSort("manual");
    try {
      await saveProjectOrder(next);
    } catch (error) {
      reportError(error);
      setProjectTransferNotice(
        `排序未儲存：${error instanceof Error ? error.message : "未知錯誤"}`,
      );
    }
  };

  const moveProjectTo = async (name: string, newParent: string) => {
    setProjectDropTarget(null);
    setProjectDropEdge(null);
    if (!name || name === newParent || newParent.startsWith(`${name}/`)) return;
    try {
      const result = await api.moveProject(name, newParent);
      setProjectTransferNotice(`已搬移專案至 ${result.name}`);
      const next = new Set(expandedProjects);
      next.delete(name);
      next.add(result.name);
      if (newParent) next.add(newParent);
      persistExpandedProjects(next);
      await refreshProjects();
      if (result.active) await loadAll();
    } catch (error) {
      reportError(error);
      setProjectTransferNotice(
        `搬移失敗：${error instanceof Error ? error.message : "未知錯誤"}`,
      );
    }
  };

  const moveProjectAcrossWorkspace = async (
    name: string,
    targetWorkspace: string,
    targetLabel: string,
  ) => {
    setProjectDropTarget(null);
    setProjectDropEdge(null);
    if (!name || targetWorkspace === workspace || switchingWorkspace) return;
    setSwitchingWorkspace(targetWorkspace);
    try {
      const result = await moveProjectToWorkspace(name, targetWorkspace);
      setProjectTransferNotice(`已把 ${result.name} 搬到 ${targetLabel}`);
    } catch (error) {
      reportError(error);
      setProjectTransferNotice(
        `搬移失敗：${error instanceof Error ? error.message : "未知錯誤"}`,
      );
    } finally {
      setSwitchingWorkspace(null);
    }
  };

  return {
    projectDropTarget,
    setProjectDropTarget,
    projectDropEdge,
    setProjectDropEdge,
    moveProjectTo,
    moveProjectAcrossWorkspace,
    reorderProject,
  };
}
