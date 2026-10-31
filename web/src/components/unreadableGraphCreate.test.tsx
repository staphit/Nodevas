import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { translate } from "../i18n";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import type { Tab } from "../state/types";
import type { Graph } from "../types";
import { BoardToolbar, type BoardToolbarProps } from "./BoardToolbar";
import { CommandPalette } from "./CommandPalette";
import { TabBody } from "./Drawer";
import { openCommandPalette } from "./topbar/actions";

// While graph.yaml cannot be read the board hides New node, but the same
// writes were still a keystroke away: Ctrl+K "New node" (POST /api/nodes 400,
// then the hub's sentence a third time on the error bar), ⋯ › Align all
// ("all nodes are already on the grid" about a graph it cannot read), and a
// node opened from the Explorer with a fully editable panel.

const HUB_PARSE_EN =
  "graph.yaml: line 2 has an error. Fix the file, then reload. (yaml: line 2: did not find expected ',' or ']')";

const GRAPH: Graph = {
  version: 1,
  nodes: [{ id: "design", title: "Design" }],
  edges: [],
  ui: {},
};

const loaded: Tab = {
  id: "design",
  pinned: false,
  content: "# Design\n",
  rev: "r1",
  dirty: false,
  loaded: true,
  diskChanged: false,
  draftPending: null,
  conflict: null,
};

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "getNodeLinks").mockResolvedValue({ backlinks: [], outgoing: [] });
  vi.spyOn(api, "listNodePages").mockResolvedValue({ pages: [] } as never);
  vi.spyOn(api, "getHistory").mockResolvedValue({ versions: [] } as never);
  vi.spyOn(api, "createNode").mockResolvedValue({ id: "n9" } as never);
  useApp.setState({
    preferences: { ...readPreferences(), language: "en", projectMode: "info" },
    activeProject: "Alpha",
    projects: [],
    workspaces: [],
    graph: GRAPH,
    graphUnreadable: HUB_PARSE_EN,
    runState: { nodes: {}, history: [] },
    statuses: { design: "ready" },
    stagedLifecycle: {},
    tabs: [loaded],
    activeTab: "design",
    error: null,
    requestLock: vi.fn(),
    releaseLock: vi.fn(),
  });
});

describe("the command palette while graph.yaml can't be read", () => {
  it("offers no New node command", () => {
    render(<CommandPalette />);
    act(() => openCommandPalette());
    fireEvent.change(screen.getByRole("textbox", { name: /^Search/ }), { target: { value: "New node" } });
    expect(screen.queryByRole("option", { name: /^New node/ })).toBeNull();
  });

  it("still offers it for a graph that reads", () => {
    useApp.setState({ graphUnreadable: null });
    render(<CommandPalette />);
    act(() => openCommandPalette());
    fireEvent.change(screen.getByRole("textbox", { name: /^Search/ }), { target: { value: "New node" } });
    expect(screen.getAllByRole("option", { name: /^New node/ }).length).toBeGreaterThan(0);
  });
});

describe("creating a node while graph.yaml can't be read", () => {
  it("is refused before it is sent, saying why", async () => {
    await expect(useApp.getState().createNode({ title: "x", kind: "task" })).rejects.toThrow(
      translate("graph.error.unreadable", "en"),
    );
    expect(api.createNode).not.toHaveBeenCalled();
  });
});

describe("the board's More graph tools while graph.yaml can't be read", () => {
  it("cannot align every node to the grid", async () => {
    const user = userEvent.setup();
    const noop = vi.fn();
    const props = {
      isGraph: true,
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
      canAlignToGrid: true,
      canCreate: false,
      selectedIDs: [],
      selectedNodeId: null,
      nodeTitle: () => "",
      gestureMode: "pan",
      setGestureMode: noop,
      snapGuides: false,
      setSnapGuides: noop,
      openNodeCreationMenu: noop,
      viewportCreationAnchor: noop,
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
    } as unknown as BoardToolbarProps;
    render(<BoardToolbar {...props} />);
    await user.click(screen.getByRole("button", { name: "More graph tools" }));
    expect(screen.getByRole("menuitem", { name: /^Align all/ })).toBeDisabled();
  });
});

describe("a node's panel while graph.yaml can't be read", () => {
  it("says why and is read-only: no status, assignee or property edits", () => {
    render(<TabBody id="design" />);
    expect(screen.getByText(translate("drawer.graphUnreadable", "en"))).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: translate("lifecycle.title", "en") })).toBeNull();
    expect(screen.queryByText(translate("drawer.moreProperties", "en"))).toBeNull();
    const content = document.querySelector(".cm-content");
    expect(content).toHaveAttribute("aria-readonly", "true");
  });

  it("stays editable for a graph that reads", () => {
    useApp.setState({ graphUnreadable: null });
    render(<TabBody id="design" />);
    expect(screen.getByText(translate("drawer.moreProperties", "en"))).toBeInTheDocument();
    expect(document.querySelector(".cm-content")).not.toHaveAttribute("aria-readonly");
  });
});
