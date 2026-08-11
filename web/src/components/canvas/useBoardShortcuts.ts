/**
 * Keyboard shortcuts for the graph board.
 *
 * Escape clears what is selected, Enter opens it, S flips an edge between
 * required and optional, and Delete removes whatever the selection points at
 * — a node, several nodes, a logic gate, a wire vertex or an edge. Deletions
 * that lose work ask first; the rest are undoable through the command layer.
 *
 * The listener is on window, so it only binds while this pane is the active
 * one and is not collapsed.
 */

import { useEffect } from "react";
import { reportError, useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import { nodeLabels, pasteNodeClipboard, stashNodes } from "./nodeTransfer";
import type { CanvasCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import type { Graph } from "../../types";
import type { GraphSelection } from "../LaneView";

export function useBoardShortcuts({
  isGraph,
  collapsed,
  keyboardActive,
  graph,
  graphSelection,
  setGraphSelection,
  selectedEdgeEndpoints,
  marqueeHandlers,
  onSelectedNodeChange,
  openTab,
  deleteNode,
  deleteNodes,
  updateCanvasLayout,
  toggleEdgeStyle,
  shortcutBusy,
  setShortcutBusy,
}: {
  isGraph: boolean;
  collapsed: boolean;
  keyboardActive: boolean;
  graph: Graph | null;
  graphSelection: GraphSelection;
  setGraphSelection: (selection: GraphSelection) => void;
  selectedEdgeEndpoints: { from: string; to: string } | null;
  marqueeHandlers: { cancel: () => boolean };
  onSelectedNodeChange?: (id: string | null) => void;
  openTab: (id: string) => Promise<unknown>;
  deleteNode: (id: string) => Promise<unknown>;
  deleteNodes: (ids: string[]) => Promise<unknown>;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;
  toggleEdgeStyle: (from: string, to: string) => Promise<void>;
  /** Guards the async deletes: one destructive key press at a time. */
  shortcutBusy: boolean;
  setShortcutBusy: (busy: boolean) => void;
}) {

  useEffect(() => {
    if (!isGraph || collapsed || !keyboardActive) return;
    const onShortcut = async (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (
        target?.isContentEditable ||
        target?.closest("input, textarea, select, [contenteditable='true'], .cm-editor")
      ) {
        return;
      }
      if (event.key === "Escape") {
        if (marqueeHandlers.cancel()) {
          event.preventDefault();
        }
        if (graphSelection) {
          event.preventDefault();
          setGraphSelection(null);
          onSelectedNodeChange?.(null);
        }
        return;
      }
      if (event.key === "Enter" && graphSelection?.kind === "node") {
        event.preventDefault();
        await openTab(graphSelection.id).catch(reportError);
        return;
      }
      if (
        event.key.toLowerCase() === "s" &&
        selectedEdgeEndpoints &&
        !shortcutBusy
      ) {
        event.preventDefault();
        try {
          await toggleEdgeStyle(
            selectedEdgeEndpoints.from,
            selectedEdgeEndpoints.to,
          );
        } catch (error) {
          reportError(error);
        }
        return;
      }
      // Ctrl/⌘+C, X and V move a selection between projects. Cut only
      // remembers: nothing is removed until a paste actually lands the nodes
      // somewhere, so an unpasted cut costs nothing.
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey) {
        const key = event.key.toLowerCase();
        const selected =
          graphSelection?.kind === "node"
            ? [graphSelection.id]
            : graphSelection?.kind === "nodes"
              ? graphSelection.ids
              : [];
        if ((key === "c" || key === "x") && selected.length > 0) {
          // A real text selection is what the user meant to copy; the board
          // only claims the shortcut when nothing is selected as text.
          if ((window.getSelection()?.toString() ?? "") !== "") return;
          event.preventDefault();
          stashNodes(
            selected,
            nodeLabels(graph, selected),
            key === "x" ? "cut" : "copy",
          );
          return;
        }
        if (key === "v" && useApp.getState().nodeClipboard) {
          event.preventDefault();
          if (shortcutBusy) return;
          setShortcutBusy(true);
          try {
            await pasteNodeClipboard();
          } catch (error) {
            reportError(error);
          } finally {
            setShortcutBusy(false);
          }
          return;
        }
      }
      if (
        shortcutBusy ||
        !graphSelection ||
        (event.key !== "Delete" && event.key !== "Backspace")
      ) {
        return;
      }
      event.preventDefault();
      if (graphSelection.kind === "vertex") {
        const selected = graphSelection;
        setShortcutBusy(true);
        try {
          const result = await updateCanvasLayout({
            type: "canvas.removeWireVertex",
            wireKey: selected.wireKey,
            index: selected.index,
          });
          if (!result.ok) throw new Error(result.message);
          setGraphSelection(null);
        } catch (error) {
          reportError(error);
        } finally {
          setShortcutBusy(false);
        }
        return;
      }
      if (graphSelection.kind === "node") {
        const node = (graph?.nodes ?? []).find((item) => item.id === graphSelection.id);
        const confirmed = await confirmAction({
          title: "刪除節點",
          description: `確定刪除「${
            node?.title || graphSelection.id
          }」？節點會移到垃圾桶，可隨時復原。`,
          confirmLabel: "移到垃圾桶",
          tone: "danger",
        });
        if (!confirmed) {
          return;
        }
        setShortcutBusy(true);
        try {
          await deleteNode(graphSelection.id);
          setGraphSelection(null);
          onSelectedNodeChange?.(null);
        } catch (error) {
          reportError(error);
        } finally {
          setShortcutBusy(false);
        }
        return;
      }
      if (graphSelection.kind === "nodes") {
        const selected = graphSelection.ids;
        const confirmed = await confirmAction({
          title: `刪除 ${selected.length} 個節點`,
          description:
            `確定將選取的 ${selected.length} 個節點移到垃圾桶？` +
            "一次撤銷即可全部復原，也可以從側欄逐一還原。",
          confirmLabel: "移到垃圾桶",
          tone: "danger",
        });
        if (!confirmed) return;
        setShortcutBusy(true);
        try {
          // One request for the whole selection: a partial delete would leave
          // the board in a state nobody asked for.
          await deleteNodes(selected);
          setGraphSelection(null);
          onSelectedNodeChange?.(null);
        } catch (error) {
          reportError(error);
        } finally {
          setShortcutBusy(false);
        }
        return;
      }
      if (graphSelection.kind === "logic-gate") {
        const selected = graphSelection;
        setShortcutBusy(true);
        try {
          const result = await updateCanvasLayout({
            type: "canvas.removeLogicGate",
            gateId: selected.id,
          });
          if (!result.ok) throw new Error(result.message);
          setGraphSelection(null);
        } catch (error) {
          reportError(error);
        } finally {
          setShortcutBusy(false);
        }
        return;
      }
      // One wire or a marquee's worth of them: the same delete either way.
      const targets =
        graphSelection.kind === "edges"
          ? graphSelection.edges
          : [{ from: graphSelection.from, to: graphSelection.to }];
      const confirmed = await confirmAction({
        title: targets.length > 1 ? `刪除 ${targets.length} 條關係線` : "刪除關係線",
        description:
          targets.length > 1
            ? `確定移除選取的 ${targets.length} 條關係？`
            : `確定移除 ${targets[0].from} → ${targets[0].to} 的關係？`,
        confirmLabel: "刪除關係",
        tone: "danger",
      });
      if (!confirmed) return;
      setShortcutBusy(true);
      try {
        for (const target of targets) {
          const result = await updateCanvasLayout({
            type: "canvas.removeEdge",
            from: target.from,
            to: target.to,
          });
          if (!result.ok) throw new Error(result.message);
        }
        setGraphSelection(null);
      } catch (error) {
        reportError(error);
      } finally {
        setShortcutBusy(false);
      }
    };
    window.addEventListener("keydown", onShortcut);
    return () => window.removeEventListener("keydown", onShortcut);
  }, [
    collapsed,
    deleteNode,
    deleteNodes,
    graph,
    graphSelection,
    isGraph,
    keyboardActive,
    openTab,
    onSelectedNodeChange,
    updateCanvasLayout,
    selectedEdgeEndpoints,
    shortcutBusy,
    toggleEdgeStyle,
  ]);
}
