import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { COL_W, ROW_H, type Col } from "./geometry";
import { MAX_COL, MAX_ROW, useBoardColumns } from "./useBoardColumns";
import type { Graph, StatusDefinition } from "../../types";

const customDone: StatusDefinition = {
  id: "custom-status-closed",
  label: "已結案",
  color: "#666",
  shape: "circle",
  settled: true,
};

const graph: Graph = {
  version: 1,
  nodes: [
    { id: "done", title: "已完成" },
    { id: "active", title: "進行中" },
    { id: "closed", title: "已結案" },
    { id: "selected", title: "目前節點" },
  ],
  edges: [],
  ui: {
    timelineOrder: ["done", "active", "closed", "selected"],
    customStatuses: [customDone],
  },
};

const baseProps = {
  graph,
  statuses: {
    done: "done" as const,
    closed: customDone.id,
  },
  timelineSort: "manual" as const,
  timelineCellWidth: 200,
  timelineCellHeight: 100,
};

describe("useBoardColumns timeline order", () => {
  it("keeps settled nodes at the bottom while leaving active nodes in order", () => {
    const { result } = renderHook(() => useBoardColumns(baseProps));

    expect(result.current.timelineCols.map((column) => column.node.id)).toEqual([
      "active",
      "selected",
      "done",
      "closed",
    ]);
  });

  it("puts the selected node first, even when it is settled", () => {
    const { result } = renderHook(() =>
      useBoardColumns({ ...baseProps, selectedNodeId: "done" }),
    );

    expect(result.current.timelineCols.map((column) => column.node.id)).toEqual([
      "done",
      "active",
      "selected",
      "closed",
    ]);
  });
});

/** Pairs of cards whose drawn rectangles intersect, at most `limit` of them. */
function overlappingPairs(cols: Col[], limit = 5): string[] {
  const pairs: string[] = [];
  for (let a = 0; a < cols.length && pairs.length < limit; a += 1) {
    for (let b = a + 1; b < cols.length && pairs.length < limit; b += 1) {
      const left = cols[a];
      const right = cols[b];
      if (
        Math.abs((left.index - right.index) * COL_W) < (left.width + right.width) / 2 &&
        Math.abs((left.row - right.row) * ROW_H) < (left.height + right.height) / 2
      ) {
        pairs.push(`${left.node.id}@${left.index},${left.row} / ${right.node.id}@${right.index},${right.row}`);
      }
    }
  }
  return pairs;
}

/** 2000 nodes, 792 of them roots, the rest chained off earlier nodes; no saved positions. */
function wideGraph(): Graph {
  const nodes = Array.from({ length: 2000 }, (_, index) => ({
    id: `n${index}`,
    title: `Card ${index}`,
  }));
  const edges = nodes.slice(792).map((node, index) => ({ from: `n${index}`, to: node.id }));
  return { version: 1, nodes, edges };
}

const layoutProps = {
  statuses: {},
  timelineSort: "manual" as const,
  timelineCellWidth: 200,
  timelineCellHeight: 100,
};

describe("useBoardColumns graph layout", () => {
  it("never stacks cards when a row has more nodes than the board is wide", () => {
    const { result } = renderHook(() => useBoardColumns({ ...layoutProps, graph: wideGraph() }));
    const { cols } = result.current;

    expect(cols).toHaveLength(2000);
    expect(overlappingPairs(cols)).toEqual([]);
    for (const col of cols) {
      expect(col.index).toBeGreaterThanOrEqual(0);
      expect(col.index).toBeLessThanOrEqual(MAX_COL);
      expect(col.row).toBeGreaterThanOrEqual(0);
      expect(col.row).toBeLessThanOrEqual(MAX_ROW);
    }
  });

  it("unstacks saved positions that an earlier layout wrote onto one cell", () => {
    // What a drag saved after the old layout piled the overflow on one cell.
    const graph = wideGraph();
    graph.ui = {
      positions: Object.fromEntries(
        graph.nodes.map((node, index) => [
          node.id,
          index < 500 ? { x: index, y: 0 } : { x: 499.976, y: 0 },
        ]),
      ),
    };
    const { result } = renderHook(() => useBoardColumns({ ...layoutProps, graph }));

    expect(overlappingPairs(result.current.cols)).toEqual([]);
    // Cards that did not collide keep the place they were saved at.
    expect(result.current.colOf.get("n0")).toMatchObject({ index: 0, row: 0 });
    expect(result.current.colOf.get("n498")).toMatchObject({ index: 498, row: 0 });
  });
});
