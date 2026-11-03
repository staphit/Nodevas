import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

const demo = { name: "demo", label: "demo", depth: 0, path: "/ws/demo", nodes: 1 };
const other = { name: "other", label: "other", depth: 0, path: "/ws/other", nodes: 1 };

function seed() {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: { version: 1, nodes: [{ id: "a", title: "Draft" }], edges: [], ui: {} },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [demo, other],
    activeProject: "demo",
    activeTab: null,
    openTab: vi.fn(async () => {}),
    nodeFolders: [],
    nodeFolderOf: {},
    refreshProjects: vi.fn(async () => {}),
    // Creating opens the new project, as the store's switch does.
    switchProject: vi.fn(async (name: string) => {
      useApp.setState((state) => ({
        activeProject: name,
        projects: state.projects.some((project) => project.name === name)
          ? state.projects
          : [...state.projects, { name, label: name, depth: 0, path: `/ws/${name}`, nodes: 0 }],
      }));
    }),
  });
}

async function createProject(name: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /New project/ }));
  await user.type(await screen.findByLabelText("Project name"), name);
  await user.click(screen.getByRole("button", { name: "Create and open" }));
}

// The explorer's notice line says what the last project action did. It said
// "Created and opened “Sweep Quad”." for good: after an example was opened
// and through every switch since, with another project open.
describe("Explorer project notice", () => {
  beforeEach(() => seed());

  it("says what was created, in the project it opened", async () => {
    render(<Sidebar />);
    await createProject("Sweep Quad");
    expect(await screen.findByRole("status")).toHaveTextContent("Created and opened “Sweep Quad”.");
    expect(useApp.getState().activeProject).toBe("Sweep Quad");
  });

  it("goes once another project is opened", async () => {
    render(<Sidebar />);
    await createProject("Sweep Quad");
    await screen.findByText("Created and opened “Sweep Quad”.");

    act(() => useApp.setState({ activeProject: "other" }));
    await waitFor(() =>
      expect(screen.queryByText("Created and opened “Sweep Quad”.")).not.toBeInTheDocument(),
    );
    // Gone, not hidden: coming back does not bring it back.
    act(() => useApp.setState({ activeProject: "Sweep Quad" }));
    expect(screen.queryByText("Created and opened “Sweep Quad”.")).not.toBeInTheDocument();
  });
});
