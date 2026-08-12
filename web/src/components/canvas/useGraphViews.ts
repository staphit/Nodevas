/**
 * Filtering the board, and acting on what the filter left.
 *
 * A view is a filter plus a sort, saveable by name so a way of looking at the
 * project can be returned to. Nodes that fall outside it stay on the canvas
 * dimmed rather than disappearing, because their dependencies still matter.
 * The batch edits act on the current selection, one command per node.
 */

import { useState } from "react";
import { useI18n } from "../../i18n";
import { reportError } from "../../store";
import type { CanvasCommand, NodeCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import type { GraphNode, SavedView, Status } from "../../types";

export function useGraphViews({
  statuses,
  selectedIDs,
  setShortcutBusy,
  setStatus,
  setGraphNotice,
  updateNode,
  updateCanvasLayout,
  runCanvasCommand,
}: {
  statuses: Record<string, Status>;
  selectedIDs: string[];
  setShortcutBusy: (busy: boolean) => void;
  setStatus: (id: string, status: Status, by: string) => Promise<unknown>;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
  updateNode: (command: NodeCommand) => Promise<CommandResult>;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;
  runCanvasCommand: (command: CanvasCommand) => void;
}) {
  const { t } = useI18n();
  const [savedViewName, setSavedViewName] = useState("");
  const [viewFilter, setViewFilter] = useState<{
    query: string;
    status: string;
    assignee: string;
    tag: string;
    priority: string;
    sort: "manual" | "title" | "priority" | "status" | "assignee";
  }>({
    query: "",
    status: "",
    assignee: "",
    tag: "",
    priority: "",
    sort: "manual",
  });

  const matchesViewFilter = (node: GraphNode) => {
    const query = viewFilter.query.trim().toLocaleLowerCase();
    if (
      query &&
      ![node.id, node.title, node.assignee, ...(node.tags ?? [])]
        .filter(Boolean)
        .some((value) => String(value).toLocaleLowerCase().includes(query))
    ) {
      return false;
    }
    if (viewFilter.status && (statuses[node.id] ?? "ready") !== viewFilter.status) return false;
    if (viewFilter.assignee && (node.assignee || "__unassigned__") !== viewFilter.assignee) {
      return false;
    }
    if (viewFilter.tag && !(node.tags ?? []).includes(viewFilter.tag)) return false;
    if (viewFilter.priority && (node.priority || "") !== viewFilter.priority) return false;
    return true;
  };

  const applyBatchStatus = (status: Status) => {
    if (!selectedIDs.length) return;
    setShortcutBusy(true);
    void Promise.all(selectedIDs.map((id) => setStatus(id, status, t("canvasOps.batchUpdateActor"))))
      .then(() =>
        setGraphNotice({
          text: t("canvasOps.statusBatchUpdated", { count: selectedIDs.length }),
          kind: "ok",
        }),
      )
      .catch(reportError)
      .finally(() => setShortcutBusy(false));
  };
  const applyBatchAssignee = (assignee: string) => {
    if (!selectedIDs.length) return;
    void updateNode({
      type: "node.assignMany",
      nodeIds: selectedIDs,
      userId: assignee || undefined,
    }).then((result) => {
      if (!result.ok) reportError(new Error(result.message));
    });
  };
  const saveCurrentView = () => {
    const name = savedViewName.trim();
    if (!name) return;
    void updateCanvasLayout({
      type: "canvas.saveView",
      view: {
        id: `view-${Date.now()}`,
        name,
        statuses: viewFilter.status ? [viewFilter.status] : undefined,
        assignees: viewFilter.assignee ? [viewFilter.assignee] : undefined,
        tags: viewFilter.tag ? [viewFilter.tag] : undefined,
        priorities: viewFilter.priority ? [viewFilter.priority] : undefined,
        sort: viewFilter.sort,
      },
    }).then((result) => {
      if (!result.ok) {
        reportError(new Error(result.message));
        return;
      }
      setSavedViewName("");
    });
  };
  const applySavedView = (view: SavedView) => {
    setViewFilter((current) => ({
      ...current,
      status: view.statuses?.[0] ?? "",
      assignee: view.assignees?.[0] ?? "",
      tag: view.tags?.[0] ?? "",
      priority: view.priorities?.[0] ?? "",
      sort: view.sort ?? "manual",
    }));
  };
  const removeSavedView = (id: string) => {
    runCanvasCommand({ type: "canvas.removeView", id });
  };

  return {
    savedViewName,
    setSavedViewName,
    viewFilter,
    setViewFilter,
    matchesViewFilter,
    applyBatchStatus,
    applyBatchAssignee,
    saveCurrentView,
    applySavedView,
    removeSavedView,
  };
}
