import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp, usePreference } from "../store";
import { Sidebar } from "./Sidebar";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function seed(initial: Record<string, string> = {}) {
  configurePreferenceStorage(fakeStorage(initial));
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [{ id: "a", title: "Draft" }], edges: [], ui: {} },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [{ name: "demo", label: "demo", depth: 0, path: "/ws/demo", nodes: 1 }],
    activeProject: "demo",
    activeTab: null,
    openTab: vi.fn(async () => {}),
    nodeFolders: [],
    nodeFolderOf: {},
  });
}

describe("Sidebar tools", () => {
  beforeEach(() => seed());

  it("shows one action up front and folds the rest", () => {
    render(<Sidebar />);

    expect(screen.getByRole("button", { name: /New project/ })).toBeInTheDocument();
    const more = screen.getByRole("button", { name: "More project actions" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    for (const name of [/New subproject/, /Import MD/, /Connect workspace/, "Save as"]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
    expect(screen.queryByRole("combobox", { name: "Project sort order" })).not.toBeInTheDocument();
  });

  it("unfolds every tool, including the project-file row, and remembers it", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(screen.getByRole("button", { name: "More project actions" }));

    for (const name of [/New subproject/, /Import MD/, /Connect workspace/, /Open an example/, "Save as"]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(screen.getByRole("combobox", { name: "Project sort order" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import source" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export target" })).toBeInTheDocument();
    expect(useApp.getState().preferences.explorerToolsExpanded).toBe(true);
  });

  // The explorer is projects and nodes; validation warnings live in the
  // board's Problems panel now.
  it("lists projects and nodes without a validation section", () => {
    useApp.setState({ issues: [{ severity: "error", nodeId: "a", msg: "Broken" }] });
    render(<Sidebar />);
    expect(screen.getByRole("button", { name: /Draft/ })).toBeInTheDocument();
    expect(screen.queryByText("Validation")).not.toBeInTheDocument();
    expect(screen.queryByText(/Broken/)).not.toBeInTheDocument();
  });

  it("folds itself from its own heading", async () => {
    const user = userEvent.setup();
    useApp.getState().updateUIPreference("explorerCollapsed", false);
    render(<Sidebar />);
    await user.click(screen.getByRole("button", { name: "Collapse project explorer" }));
    expect(useApp.getState().preferences.explorerCollapsed).toBe(true);
  });

  // The « leaves with the explorer, so the keyboard goes to the board's ».
  it("hands focus to the board's way back once folded", async () => {
    const user = userEvent.setup();
    useApp.getState().updateUIPreference("explorerCollapsed", false);
    function Shell() {
      const collapsed = usePreference("explorerCollapsed");
      return collapsed ? (
        <button type="button" className="board-explorer-toggle">
          »
        </button>
      ) : (
        <Sidebar />
      );
    }
    render(<Shell />);
    await user.click(screen.getByRole("button", { name: "Collapse project explorer" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "»" })).toHaveFocus());
  });

  // The workspace context menu clicks these inputs too, so they must exist
  // while the row that normally fronts them is folded away.
  it("keeps the import inputs mounted while folded", () => {
    const { container } = render(<Sidebar />);
    expect(container.querySelectorAll('input[type="file"]').length).toBeGreaterThanOrEqual(3);
  });

  // Creating nodes writes graph.yaml, the file the reader has to fix; the
  // Import menu's Markdown and JSON Canvas items stand down for that, and so
  // does the big button in front of them.
  it("disables Import MD while graph.yaml cannot be read", async () => {
    const user = userEvent.setup();
    useApp.setState({ graphUnreadable: "graph.yaml: line 41 has an error." });
    render(<Sidebar />);
    await user.click(screen.getByRole("button", { name: "More project actions" }));
    expect(screen.getByRole("button", { name: /Import MD/ })).toBeDisabled();
    useApp.setState({ graphUnreadable: null });
    await waitFor(() => expect(screen.getByRole("button", { name: /Import MD/ })).toBeEnabled());
  });
});
