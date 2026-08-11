/**
 * Keeping the board and the rest of the app pointing at the same node: the
 * pane's selection follows the document being edited, a reveal request scrolls
 * that node into view, and a selection whose target has gone away is dropped.
 *
 * Both panes mount this; each effect decides for itself which one it belongs
 * to, so the two never fork.
 */

import { useEffect, type RefObject } from "react";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { ROW_H, type Col } from "../canvas/geometry";
import type { GraphSelection } from "../LaneView";
import { RULER_W } from "./boardGeometry";

export function useBoardReveal({
  isGraph,
  collapsed,
  boardRef,
  graph,
  colOf,
  timelineColOf,
  timelineCellWidth,
  zoom,
  centerX,
  rowTop,
  selectedNodeId,
  onSelectedNodeChange,
  graphSelection,
  setGraphSelection,
}: {
  isGraph: boolean;
  collapsed?: boolean;
  boardRef: RefObject<HTMLDivElement | null>;
  graph: Graph | null;
  colOf: Map<string, Col>;
  timelineColOf: Map<string, Col>;
  timelineCellWidth: number;
  zoom: number;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  selectedNodeId?: string | null;
  onSelectedNodeChange?: (nodeId: string | null) => void;
  graphSelection: GraphSelection;
  setGraphSelection: React.Dispatch<React.SetStateAction<GraphSelection>>;
}) {
  useEffect(() => {
    if (!isGraph) return;
    if (selectedNodeId) {
      setGraphSelection((current) =>
        current?.kind === "node" && current.id === selectedNodeId
          ? current
          : { kind: "node", id: selectedNodeId },
      );
    } else {
      setGraphSelection((current) => (current?.kind === "node" ? null : current));
    }
  }, [isGraph, selectedNodeId]);

  // Following a link opens the document; the board answers by selecting the
  // node and scrolling it into view, so the jump is visible on the canvas too.
  const revealRequest = useApp((state) => state.revealRequest);
  const clearRevealRequest = useApp((state) => state.clearRevealRequest);
  useEffect(() => {
    // The graph pane owns this: it is the one that can place a node in space.
    // Both panes mount this component, so the timeline must not consume the
    // request or the graph would never see it.
    if (!isGraph || !revealRequest || collapsed) return;
    const column = colOf.get(revealRequest.nodeId);
    const board = boardRef.current;
    if (!column || !board) return;
    setGraphSelection({ kind: "node", id: revealRequest.nodeId });
    onSelectedNodeChange?.(revealRequest.nodeId);
    board.scrollTo({
      left: Math.max(0, (centerX(column) - board.clientWidth / 2 / zoom) * zoom),
      top: Math.max(
        0,
        (rowTop(column.row) + ROW_H / 2 - board.clientHeight / 2 / zoom) * zoom,
      ),
      behavior: "smooth",
    });
    clearRevealRequest();
  }, [revealRequest, collapsed, isGraph, colOf, zoom]);

  useEffect(() => {
    if (isGraph || collapsed || !selectedNodeId) return;
    const selectedColumn = timelineColOf.get(selectedNodeId);
    const board = boardRef.current;
    if (!selectedColumn || !board) return;
    const left = selectedColumn.index * timelineCellWidth;
    const right = left + timelineCellWidth;
    const visibleLeft = board.scrollLeft;
    const visibleRight = visibleLeft + board.clientWidth - RULER_W;
    if (left >= visibleLeft && right <= visibleRight) return;
    board.scrollTo({
      left: Math.max(
        0,
        left - Math.max(0, (board.clientWidth - timelineCellWidth) / 2),
      ),
      top: board.scrollTop,
      behavior: "smooth",
    });
  }, [
    collapsed,
    isGraph,
    selectedNodeId,
    timelineCellWidth,
    timelineColOf,
  ]);

  useEffect(() => {
    if (!graphSelection || !graph) return;
    const exists =
      graphSelection.kind === "node"
        ? (graph.nodes ?? []).some((node) => node.id === graphSelection.id)
        : graphSelection.kind === "nodes"
          ? graphSelection.ids.length > 0 &&
            graphSelection.ids.every((id) =>
              (graph.nodes ?? []).some((node) => node.id === id),
            )
        : graphSelection.kind === "logic-gate"
          ? (graph.ui?.logicGates ?? []).some((gate) => gate.id === graphSelection.id)
          : graphSelection.kind === "edge"
            ? (graph.edges ?? []).some(
              (edge) => edge.from === graphSelection.from && edge.to === graphSelection.to,
            )
            : graphSelection.kind === "edges"
              ? graphSelection.edges.some((target) =>
                  (graph.edges ?? []).some(
                    (edge) => edge.from === target.from && edge.to === target.to,
                  ),
                )
            : (graph.ui?.wireVertices?.[graphSelection.wireKey]?.length ?? 0) >
              graphSelection.index;
    if (!exists) {
      if (
        graphSelection.kind === "node" &&
        selectedNodeId === graphSelection.id
      ) {
        onSelectedNodeChange?.(null);
      }
      setGraphSelection(null);
    }
  }, [graph, graphSelection, onSelectedNodeChange, selectedNodeId]);
}
