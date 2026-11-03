import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../i18n";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import { Sidebar } from "./Sidebar";

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

const LONG = "ai-harness-engineering";

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [], edges: [], ui: {} },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [
      { name: LONG, label: LONG, depth: 0, path: `/ws/${LONG}`, nodes: 4 },
      { name: "archive", label: "archive", depth: 0, path: "/ws/archive", nodes: 0, isFolder: true },
    ],
    activeProject: LONG,
    activeTab: null,
    openTab: vi.fn(async () => {}),
    nodeFolders: [],
    nodeFolderOf: {},
    refreshProjects: vi.fn(async () => {}),
  });
});

// A long project name is cut with an ellipsis in the explorer. The row's own
// tooltip is the one that shows over its padding and tags and on keyboard
// focus, so it carries the full name ahead of the selection hint.
describe("a project row's tooltip", () => {
  it("leads with the full project name", () => {
    const { container } = render(<Sidebar />);
    const rows = Array.from(container.querySelectorAll<HTMLButtonElement>(".project-tree-open"));
    const project = rows.find((row) => row.textContent?.includes(LONG))!;
    expect(project.title).toBe(`${LONG}\n${translate("explorer.projectSelectionTitle", "en")}`);
    const folder = rows.find((row) => row.textContent?.includes("archive"))!;
    expect(folder.title).toBe(`archive\n${translate("explorer.folderProjectTitle", "en")}`);
  });
});
