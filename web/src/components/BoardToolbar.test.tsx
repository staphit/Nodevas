import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { BoardToolbar, type BoardToolbarProps } from "./BoardToolbar";

function props(over: Partial<BoardToolbarProps> = {}): BoardToolbarProps {
  const noop = vi.fn();
  const anchor = () => ({ kind: "graph" as const, x: 0, y: 0, col: 0, row: 0 });
  return {
    isGraph: false,
    collapsed: false,
    onToggle: noop,
    zoom: 1,
    zoomAt: noop,
    nodeScale: 1,
    setNodeScale: noop,
    cardFontScale: 1,
    setCardFontScale: noop,
    resetCardScale: noop,
    filterPanel: null,
    activeFilters: 0,
    problemCount: 0,
    problemsOpen: false,
    setProblemsOpen: noop,
    fitGraph: noop,
    snapToGrid: false,
    setSnapToGrid: noop,
    minimapVisible: false,
    setMinimapVisible: noop,
    alignAllToGrid: noop,
    canAlignToGrid: false,
    selectedIDs: [],
    selectedNodeId: null,
    nodeTitle: (id) => (id === "n1" ? "Client follow-up with the whole sales team" : ""),
    gestureMode: "pan" as BoardToolbarProps["gestureMode"],
    setGestureMode: noop,
    snapGuides: false,
    setSnapGuides: noop,
    openNodeCreationMenu: noop,
    viewportCreationAnchor: anchor as unknown as BoardToolbarProps["viewportCreationAnchor"],
    timelineOrientation: "vertical",
    changeTimelineOrientation: noop,
    timelineDateAxis: "top",
    changeTimelineDateAxis: noop,
    timelineNodeAxis: "left",
    changeTimelineNodeAxis: noop,
    timelineVNodeAxis: "top",
    changeTimelineVNodeAxis: noop,
    timelineVDateAxis: "right",
    changeTimelineVDateAxis: noop,
    timelineCellAuto: true,
    timelineCellWidth: 200,
    timelineCellHeight: 100,
    setTimelineCellSize: noop,
    useAutoTimelineCellSize: noop,
    collapseEmpty: false,
    setCollapse: noop,
    graphNotice: null,
    planNotice: null,
    ...over,
  };
}

beforeEach(() => {
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [], edges: [], ui: {} },
  });
});

// On a phone the timeline's header is cut to "Timeline: plann…" (at 150%
// text, to a word or two), and the selected node's line beside it likewise;
// neither said the rest anywhere.
describe("Board toolbar", () => {
  it("gives the pane's name its whole text on hover", () => {
    const { container } = render(<BoardToolbar {...props()} />);
    const title = container.querySelector(".board-title-main");
    expect(title).toHaveTextContent("Timeline: planned and actual records");
    expect(title).toHaveAttribute("title", "Timeline: planned and actual records");
  });

  it("gives the selected node's line its whole text on hover", () => {
    const { container } = render(<BoardToolbar {...props({ selectedNodeId: "n1" })} />);
    const line = container.querySelector(".board-title-message.selection");
    expect(line).toHaveAttribute("title", line?.textContent ?? "");
    expect(line?.getAttribute("title")).toContain("Client follow-up with the whole sales team");
  });
});
