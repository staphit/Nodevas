import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useBoardColumns } from "./useBoardColumns";
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
