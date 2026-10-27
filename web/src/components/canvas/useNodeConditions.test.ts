import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Graph } from "../../types";
import type { LaneContextMenu } from "../LaneView";
import { useNodeConditions } from "./useNodeConditions";

const graph = {
  nodes: [
    { id: "a", title: "A" },
    { id: "b", title: "B" },
    { id: "c", title: "C" },
    { id: "either", title: "Either", requires: "a or b" },
    { id: "both", title: "Both", requires: "a and b" },
  ],
  edges: [
    { from: "a", to: "either" },
    { from: "b", to: "either" },
    { from: "a", to: "both" },
    { from: "b", to: "both" },
  ],
} as unknown as Graph;

describe("useNodeConditions", () => {
  it("resets the new-dependency draft when a card's own menu opens on another node", () => {
    const updateNode = vi.fn(async () => ({ ok: true as const }));
    const { result, rerender } = renderHook(
      ({ contextMenu }: { contextMenu: LaneContextMenu | null }) =>
        useNodeConditions({
          graph,
          logicGates: [],
          contextMenu,
          setContextMenu: vi.fn(),
          setGraphSelection: vi.fn(),
          setGraphNotice: vi.fn(),
          updateNode: updateNode as never,
          updateCanvasLayout: vi.fn() as never,
          runCanvasCommand: vi.fn(),
        }),
      { initialProps: { contextMenu: null as LaneContextMenu | null } },
    );

    // Opened straight from the card (not through openConditionMenu).
    rerender({ contextMenu: { kind: "node", nodeId: "either", x: 0, y: 0 } });
    expect(result.current.conditionOperator).toBe("or");
    act(() => result.current.setConditionRelation("optional"));

    rerender({ contextMenu: { kind: "node", nodeId: "both", x: 0, y: 0 } });
    // The OR and the optional relation picked for "either" must not leak into
    // the plain "depends on all" node.
    expect(result.current.conditionOperator).toBe("and");
    expect(result.current.conditionRelation).toBe("");
    expect(result.current.conditionSource).toBe("c");

    act(() => result.current.addDependency("both"));
    expect(updateNode).toHaveBeenCalledWith({
      type: "node.addDependency",
      sourceId: "c",
      targetId: "both",
      operator: "and",
      relation: "",
    });
  });
});
