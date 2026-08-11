/**
 * Where every node sits on the board.
 *
 * The graph lays nodes out by dependency depth, keeping any position the user
 * has dragged them to and finding a free slot for the rest. The timeline
 * instead orders them by the saved timeline order, or by the active view's
 * sort. The focused node is temporarily promoted and settled nodes are kept
 * below active work. Both produce the same Col shape, so cards, wires and
 * gates read one layout regardless of which pane they are drawn in.
 */

import { useMemo } from "react";
import { isSettledStatus } from "../../analysis";
import type { Graph, GraphNode, Status } from "../../types";
import {
  CARD_GAP_X,
  CARD_GAP_Y,
  CARD_H,
  CARD_W,
  COL_W,
  ROW_H,
  type Col,
} from "./geometry";

/** Hard bounds on stored positions, so one bad value cannot explode the grid. */
export const MAX_COL = 499;
export const MAX_ROW = 200;

export function useBoardColumns({
  graph,
  statuses,
  timelineSort,
  selectedNodeId,
  timelineCellWidth,
  timelineCellHeight,
}: {
  graph: Graph | null;
  statuses: Record<string, Status>;
  timelineSort: "manual" | "title" | "priority" | "status" | "assignee";
  selectedNodeId?: string | null;
  timelineCellWidth: number;
  timelineCellHeight: number;
}) {
  const { cols, colOf, maxCol, maxRow } = useMemo(() => {
    const nodes = graph?.nodes ?? [];
    const edges = graph?.edges ?? [];
    const byId = new Map(nodes.map((n) => [n.id, n]));
    const children = new Map<string, string[]>();
    const incoming = new Map<string, number>();
    for (const e of edges) {
      if (!byId.has(e.from) || !byId.has(e.to)) continue;
      children.set(e.from, [...(children.get(e.from) ?? []), e.to]);
      incoming.set(e.to, (incoming.get(e.to) ?? 0) + 1);
    }
    const dfsOrder: string[] = [];
    const depthOf = new Map<string, number>();
    const visited = new Set<string>();
    const visit = (id: string, depth: number) => {
      if (visited.has(id)) {
        if (depth > (depthOf.get(id) ?? 0)) depthOf.set(id, depth);
        return;
      }
      visited.add(id);
      depthOf.set(id, depth);
      dfsOrder.push(id);
      for (const k of children.get(id) ?? []) visit(k, depth + 1);
    };
    nodes.filter((n) => !incoming.has(n.id)).forEach((r) => visit(r.id, 0));
    nodes.forEach((n) => {
      if (!visited.has(n.id)) {
        visited.add(n.id);
        depthOf.set(n.id, 0);
        dfsOrder.push(n.id);
      }
    });

    const stored = graph?.ui?.positions ?? {};
    const posOf = new Map<string, { col: number; row: number }>();
    for (const [id, p] of Object.entries(stored)) {
      if (
        byId.has(id) &&
        Number.isFinite(p.x) &&
        Number.isFinite(p.y) &&
        p.x >= 0 &&
        p.x <= MAX_COL &&
        p.y >= 0 &&
        p.y <= MAX_ROW
      ) {
        posOf.set(id, { col: p.x, row: p.y });
      }
    }
    const nodeSize = (id: string) => {
      const style = graph?.ui?.nodeStyles?.[id];
      return {
        width: Math.max(120, Math.min(360, style?.width ?? CARD_W)),
        height: Math.max(52, Math.min(180, style?.height ?? CARD_H)),
      };
    };
    const cols: Col[] = [];
    const placed = new Map<string, Col>();
    const freeColumn = (preferred: number, row: number, width: number, height: number) => {
      for (let offset = 0; offset <= MAX_COL; offset++) {
        const candidate = (preferred + offset) % (MAX_COL + 1);
        const overlaps = cols.some(
          (column) =>
            Math.abs((candidate - column.index) * COL_W) <
              (width + column.width) / 2 + CARD_GAP_X &&
            Math.abs((row - column.row) * ROW_H) <
              (height + column.height) / 2 + CARD_GAP_Y,
        );
        if (!overlaps) return candidate;
      }
      return preferred;
    };
    for (const [id, p] of [...posOf.entries()].sort(
      (a, b) => a[1].col - b[1].col || a[1].row - b[1].row,
    )) {
      const col: Col = { node: byId.get(id)!, row: p.row, index: p.col, ...nodeSize(id) };
      placed.set(id, col);
      cols.push(col);
    }
    const nextColByRow = new Map<number, number>();
    for (const id of dfsOrder) {
      if (placed.has(id)) continue;
      const row = Math.min(depthOf.get(id) ?? 0, MAX_ROW);
      const size = nodeSize(id);
      const next = freeColumn(nextColByRow.get(row) ?? 0, row, size.width, size.height);
      nextColByRow.set(row, Math.min(next + 1, MAX_COL));
      const col: Col = {
        node: byId.get(id)!,
        row,
        index: next,
        ...size,
      };
      placed.set(id, col);
      cols.push(col);
    }
    cols.sort((a, b) => a.index - b.index || a.row - b.row || a.node.id.localeCompare(b.node.id));
    const maxCol = cols.reduce((m, c) => Math.max(m, c.index), 0);
    const maxRow = cols.reduce((m, c) => Math.max(m, c.row), 0);
    return { cols, colOf: placed, maxCol, maxRow };
  }, [graph]);

  // Timeline order is intentionally independent from graph coordinates.
  const { timelineCols, timelineColOf } = useMemo(() => {
    const nodes = graph?.nodes ?? [];
    const byId = new Map(nodes.map((node) => [node.id, node]));
    const seen = new Set<string>();
    const ordered: GraphNode[] = [];
    for (const id of graph?.ui?.timelineOrder ?? []) {
      const node = byId.get(id);
      if (!node || seen.has(id)) continue;
      seen.add(id);
      ordered.push(node);
    }
    for (const node of nodes) {
      if (seen.has(node.id)) continue;
      seen.add(node.id);
      ordered.push(node);
    }
    const priorityRank: Record<string, number> = { urgent: 0, high: 1, medium: 2, low: 3 };
    const customStatuses = graph?.ui?.customStatuses ?? [];
    const settled = (node: GraphNode) =>
      isSettledStatus(statuses[node.id] ?? "ready", customStatuses);
    ordered.sort((left, right) => {
      // Keep the node the user is looking at visible at the top of the timeline.
      // This is intentionally transient and does not rewrite the saved order.
      const selectedRank =
        Number(right.id === selectedNodeId) - Number(left.id === selectedNodeId);
      if (selectedRank !== 0) return selectedRank;

      // Finished work is useful context, but should not crowd out active work.
      // Stable sorting preserves the saved/manual order within each group.
      const settledRank = Number(settled(left)) - Number(settled(right));
      if (settledRank !== 0) return settledRank;

      if (timelineSort === "title") {
        return (left.title || left.id).localeCompare(right.title || right.id, "zh-Hant");
      }
      if (timelineSort === "priority") {
        return (priorityRank[left.priority || ""] ?? 9) - (priorityRank[right.priority || ""] ?? 9);
      }
      if (timelineSort === "status") {
        return (statuses[left.id] ?? "ready").localeCompare(statuses[right.id] ?? "ready");
      }
      if (timelineSort === "assignee") {
        return (left.assignee || "\uffff").localeCompare(right.assignee || "\uffff");
      }
      return 0;
    });
    const timelineCols = ordered.map((node, index) => ({
      node,
      index,
      row: index,
      width: timelineCellWidth - CARD_GAP_X,
      height: timelineCellHeight - CARD_GAP_Y,
    }));
    return {
      timelineCols,
      timelineColOf: new Map(timelineCols.map((column) => [column.node.id, column])),
    };
  }, [
    graph,
    statuses,
    selectedNodeId,
    timelineCellHeight,
    timelineCellWidth,
    timelineSort,
  ]);

  return { cols, colOf, maxCol, maxRow, timelineCols, timelineColOf };
}
