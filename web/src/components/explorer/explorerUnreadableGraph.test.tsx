import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../../i18n";
import { configurePreferenceStorage, readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { Sidebar } from "../Sidebar";

// A page loaded while graph.yaml cannot be read has no graph, so the active
// project's Nodes read "0" over "Empty folder" though nodes/ holds three
// files: the tree said the nodes were gone instead of that it can't list them.

const HUB_PARSE_EN =
  "graph.yaml: line 2 has an error. Fix the file, then reload. (yaml: line 2: did not find expected ',' or ']')";

beforeEach(() => {
  const map = new Map<string, string>();
  configurePreferenceStorage({
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  });
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: null,
    graphUnreadable: HUB_PARSE_EN,
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [{ name: "Alpha", label: "Alpha", depth: 0, path: "/ws/Alpha", nodes: 3 }],
    activeProject: "Alpha",
    activeTab: null,
    openTab: vi.fn(async () => undefined),
    nodeFolders: [],
    nodeFolderOf: {},
  });
});

/** The active project's Nodes row, which folds its node list. */
const nodesRow = () => document.querySelector<HTMLElement>(".project-tree-children > .tree-folder-row[aria-expanded]")!;

describe("Explorer nodes while graph.yaml can't be read", () => {
  it("says the list is unavailable rather than empty", () => {
    render(<Sidebar />);
    expect(screen.getByText(translate("explorer.nodesUnavailable", "en"))).toBeInTheDocument();
    expect(screen.queryByText(translate("explorer.emptyFolderShort", "en"))).toBeNull();
    expect(nodesRow()).toHaveTextContent("Nodes");
    expect(nodesRow().querySelector(".tree-count")).toBeNull();
  });

  it("keeps the count and folders for a graph that reads", () => {
    useApp.setState({
      graphUnreadable: null,
      graph: { version: 1, nodes: [{ id: "a", title: "A" }], edges: [], ui: {} },
    });
    render(<Sidebar />);
    expect(screen.queryByText(translate("explorer.nodesUnavailable", "en"))).toBeNull();
    expect(nodesRow().querySelector(".tree-count")).toHaveTextContent("1");
  });
});
