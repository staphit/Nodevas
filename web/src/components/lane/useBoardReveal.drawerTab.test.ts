import { act, renderHook } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../../store";
import type { GraphSelection } from "../LaneView";
import { useBoardReveal } from "./useBoardReveal";

// The drawer keeps a tab per opened node. Clicking a card moves the drawer to
// it; clicking a drawer tab has to move the board and the timeline back, or
// dependency and timeline actions aim at a node other than the open page.

function mount(isGraph: boolean, initial: GraphSelection) {
  const onSelectedNodeChange = vi.fn();
  const view = renderHook(() => {
    const [graphSelection, setGraphSelection] = useState<GraphSelection>(initial);
    useBoardReveal({
      isGraph,
      boardRef: { current: null },
      graph: null,
      colOf: new Map(),
      timelineColOf: new Map(),
      timelineCellWidth: 100,
      timelineCellHeight: 100,
      timelineOrientation: "vertical",
      timelineDateAxis: "top",
      zoom: 1,
      centerX: () => 0,
      rowTop: () => 0,
      selectedNodeId: initial?.kind === "node" ? initial.id : null,
      onSelectedNodeChange,
      graphSelection,
      setGraphSelection,
    });
    return graphSelection;
  });
  return { ...view, onSelectedNodeChange };
}

describe("the board's selection follows the drawer's tab", () => {
  beforeEach(() => {
    useApp.setState({ activeTab: "B" });
  });
  afterEach(() => {
    useApp.setState({ activeTab: null });
  });

  it("moves to the node of the tab the reader switches to", () => {
    const { result, onSelectedNodeChange } = mount(true, { kind: "node", id: "B" });
    act(() => useApp.setState({ activeTab: "A" }));
    expect(result.current).toEqual({ kind: "node", id: "A" });
    // The shared selection, which the timeline follows.
    expect(onSelectedNodeChange).toHaveBeenLastCalledWith("A");
  });

  it("keeps a multi-selection that already holds that node", () => {
    const both: GraphSelection = { kind: "nodes", ids: ["A", "B"] };
    const { result, onSelectedNodeChange } = mount(true, both);
    act(() => useApp.setState({ activeTab: "A" }));
    expect(result.current).toEqual(both);
    expect(onSelectedNodeChange).not.toHaveBeenCalled();
  });

  it("leaves the selection where it is when the drawer closes", () => {
    const { result, onSelectedNodeChange } = mount(true, { kind: "node", id: "B" });
    act(() => useApp.setState({ activeTab: null }));
    expect(result.current).toEqual({ kind: "node", id: "B" });
    expect(onSelectedNodeChange).not.toHaveBeenCalled();
  });

  it("is the graph pane's to do, so the timeline pane does not do it twice", () => {
    const { onSelectedNodeChange } = mount(false, null);
    act(() => useApp.setState({ activeTab: "A" }));
    expect(onSelectedNodeChange).not.toHaveBeenCalled();
  });
});
