/**
 * Group boxes and sticky notes on the canvas.
 *
 * Decorations are pure annotation: they hold no dependencies and never affect
 * status. They exist so a board can be read at a glance, which is why one can
 * also be drawn around whatever is currently selected.
 */

import type { CanvasCommand } from "../../domain/commands";
import type { CanvasAnnotation, CanvasGroup, Graph } from "../../types";
import { COL_W, ROW_H, type Col } from "./geometry";
import type { LaneContextMenu } from "../LaneView";

export function useCanvasDecorations({
  graph,
  cols,
  selectedGraphNodeIDs,
  runCanvasCommand,
  setContextMenu,
  setGraphNotice,
}: {
  graph: Graph | null;
  cols: Col[];
  selectedGraphNodeIDs: Set<string>;
  runCanvasCommand: (command: CanvasCommand) => void;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
}) {
  const createCanvasGroup = (menu: Extract<LaneContextMenu, { kind: "graph" }>) => {
    const item: CanvasGroup = {
      id: `group-${Date.now()}`,
      title: "新群組",
      x: menu.col * COL_W,
      y: menu.row * ROW_H,
      width: 480,
      height: 240,
      color: "#31566a",
    };
    runCanvasCommand({ type: "canvas.upsertGroup", group: item });
    setContextMenu(null);
  };

  const createCanvasAnnotation = (menu: Extract<LaneContextMenu, { kind: "graph" }>) => {
    const item: CanvasAnnotation = {
      id: `note-${Date.now()}`,
      text: "輸入註解…",
      x: menu.col * COL_W,
      y: menu.row * ROW_H,
      width: 220,
      height: 110,
      color: "#6c5a2e",
    };
    runCanvasCommand({ type: "canvas.upsertAnnotation", annotation: item });
    setContextMenu(null);
  };

  const createGroupFromSelection = () => {
    const selected = cols.filter((column) => selectedGraphNodeIDs.has(column.node.id));
    if (selected.length === 0) return;
    const left = Math.min(...selected.map((c) => c.index * COL_W + COL_W / 2 - c.width / 2));
    const right = Math.max(...selected.map((c) => c.index * COL_W + COL_W / 2 + c.width / 2));
    const top = Math.min(...selected.map((c) => c.row * ROW_H + ROW_H / 2 - c.height / 2));
    const bottom = Math.max(...selected.map((c) => c.row * ROW_H + ROW_H / 2 + c.height / 2));
    const item: CanvasGroup = {
      id: `group-${Date.now()}`,
      title: "新群組",
      x: Math.max(0, left - 24),
      y: Math.max(0, top - 40),
      width: Math.max(140, right - left + 48),
      height: Math.max(80, bottom - top + 60),
      color: "#31566a",
    };
    runCanvasCommand({ type: "canvas.upsertGroup", group: item });
    setGraphNotice({ text: `已為 ${selected.length} 個節點建立群組底圖`, kind: "ok" });
  };

  const updateCanvasDecoration = (
    kind: "group" | "annotation",
    id: string,
    patch: Partial<CanvasGroup & CanvasAnnotation>,
  ) => {
    if (kind === "group") {
      const current = graph?.ui?.groups?.find((item) => item.id === id);
      if (!current) return;
      runCanvasCommand({
        type: "canvas.upsertGroup",
        group: { ...current, ...patch },
      });
      return;
    }
    const current = graph?.ui?.annotations?.find((item) => item.id === id);
    if (!current) return;
    runCanvasCommand({
      type: "canvas.upsertAnnotation",
      annotation: { ...current, ...patch },
    });
  };

  const deleteCanvasDecoration = (kind: "group" | "annotation", id: string) => {
    runCanvasCommand(
      kind === "group"
        ? { type: "canvas.removeGroup", id }
        : { type: "canvas.removeAnnotation", id },
    );
  };

  return {
    createCanvasGroup,
    createCanvasAnnotation,
    createGroupFromSelection,
    updateCanvasDecoration,
    deleteCanvasDecoration,
  };
}
