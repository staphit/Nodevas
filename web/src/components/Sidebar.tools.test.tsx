import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
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

  // The workspace context menu clicks these inputs too, so they must exist
  // while the row that normally fronts them is folded away.
  it("keeps the import inputs mounted while folded", () => {
    const { container } = render(<Sidebar />);
    expect(container.querySelectorAll('input[type="file"]').length).toBeGreaterThanOrEqual(3);
  });
});
