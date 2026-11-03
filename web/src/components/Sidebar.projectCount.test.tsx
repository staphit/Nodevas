import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
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

const project = (name: string) => ({ name, label: name, depth: 0, path: `/ws/${name}`, nodes: 1 });

function seed(language: "en" | "zh-TW", names: string[]) {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language },
    graph: { version: 1, nodes: [], edges: [], ui: {} },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: names.map(project),
    activeProject: names[0],
    activeTab: null,
    openTab: vi.fn(async () => {}),
    nodeFolders: [],
    nodeFolderOf: {},
    refreshProjects: vi.fn(async () => {}),
  });
  useApp.getState().updateUIPreference("language", language);
}

function countLabel(container: HTMLElement): string {
  return container.querySelector(".explorer-project-count")?.textContent ?? "";
}

// The explorer header counts the workspace's projects; a workspace of one
// read "1 projects".
describe("explorer project count", () => {
  beforeEach(() => configurePreferenceStorage(fakeStorage()));

  it("says one project in the singular", () => {
    seed("en", ["daily"]);
    const { container } = render(<Sidebar />);
    expect(countLabel(container)).toBe("1 project");
  });

  it("keeps the plural for any other count", () => {
    seed("en", ["daily", "weekly"]);
    const { container } = render(<Sidebar />);
    expect(countLabel(container)).toBe("2 projects");
  });

  it("has one form in Chinese", () => {
    seed("zh-TW", ["daily"]);
    const { container } = render(<Sidebar />);
    expect(countLabel(container)).toBe("1 個專案");
  });
});
