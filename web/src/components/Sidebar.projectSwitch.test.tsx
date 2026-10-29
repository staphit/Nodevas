import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreferences, writePreference } from "../preferences";
import { useApp } from "../store";
import { Sidebar } from "./Sidebar";

// A switch takes a few hundred ms (open, loadAll, trash, projects). A second
// click inside that window was dropped without a word, so the board stayed on
// the project clicked first. The last click wins instead.

type Pending = { name: string; resolve: () => void };
let pending: Pending[] = [];
let switchProject: ReturnType<typeof vi.fn>;

function seed() {
  const map = new Map<string, string>();
  configurePreferenceStorage({
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  });
  writePreference("explorerWorkspaceExpanded", true);
  pending = [];
  switchProject = vi.fn(
    (name: string) =>
      new Promise<void>((resolve) => {
        pending.push({
          name,
          resolve: () => {
            useApp.setState({ activeProject: name });
            resolve();
          },
        });
      }),
  );
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [], edges: [], ui: {} },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [
      { name: "main", label: "main", depth: 0, path: "/ws/main", nodes: 1 },
      { name: "probs", label: "probs", depth: 0, path: "/ws/probs", nodes: 1 },
      { name: "gates", label: "gates", depth: 0, path: "/ws/gates", nodes: 1 },
    ],
    activeProject: "main",
    activeTab: null,
    nodeFolders: [],
    nodeFolderOf: {},
    switchProject: switchProject as unknown as ReturnType<typeof useApp.getState>["switchProject"],
  });
}

const projectRow = (label: string) =>
  screen.getAllByRole("button").find((button) => button.querySelector(".project-tree-name")?.textContent === label)!;

async function settle(index: number) {
  await act(async () => {
    pending[index].resolve();
    await Promise.resolve();
  });
}

describe("switching projects in quick succession", () => {
  beforeEach(() => seed());

  it("lets the last click win when one lands while a switch is under way", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(projectRow("probs"));
    expect(switchProject).toHaveBeenCalledWith("probs");
    await user.click(projectRow("gates"));
    await user.click(projectRow("main"));

    await settle(0);
    // "gates" was overtaken by "main" before it began: it is never opened.
    expect(switchProject.mock.calls.map((call) => call[0])).toEqual(["probs", "main"]);
    await settle(1);
    expect(useApp.getState().activeProject).toBe("main");
  });

  it("drops a queued switch when the project under way is clicked again", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(projectRow("probs"));
    await user.click(projectRow("gates"));
    await user.click(projectRow("probs"));

    await settle(0);
    expect(switchProject.mock.calls.map((call) => call[0])).toEqual(["probs"]);
    expect(useApp.getState().activeProject).toBe("probs");
  });
});
