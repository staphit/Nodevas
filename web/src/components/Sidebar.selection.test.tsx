import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import { Sidebar } from "./Sidebar";

function seed() {
  const map = new Map<string, string>();
  configurePreferenceStorage({
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => void map.set(key, value),
    removeItem: (key) => void map.delete(key),
  });
  useApp.setState({
    preferences: { ...readPreferences(), language: "en" },
    graph: {
      version: 1,
      nodes: [
        { id: "a", title: "Inbox triage" },
        { id: "b", title: "Sweep node A" },
      ],
      edges: [],
      ui: {},
    },
    statuses: {},
    issues: [],
    trash: [],
    workspace: "/ws",
    workspaces: [],
    projects: [{ name: "demo", label: "demo", depth: 0, path: "/ws/demo", nodes: 2 }],
    activeProject: "demo",
    activeTab: null,
    // Opening a node is what makes it the current one.
    openTab: vi.fn(async (id: string) => useApp.setState({ activeTab: id })),
    nodeFolders: [],
    nodeFolderOf: {},
  });
}

const row = (name: RegExp) => screen.getByRole("button", { name });

describe("Sidebar node selection", () => {
  beforeEach(() => seed());

  // A node picked on the board is the current one; the tree must not keep a
  // second row outlined, which drag-to-folder and Ctrl/Shift-click would use.
  it("follows the current node when it is picked outside the tree", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(row(/Inbox triage/));
    expect(row(/Inbox triage/)).toHaveClass("active", "selected");

    act(() => useApp.setState({ activeTab: "b" }));

    expect(row(/Inbox triage/)).not.toHaveClass("selected");
    expect(row(/Inbox triage/)).not.toHaveClass("active");
    expect(row(/Sweep node A/)).toHaveClass("active", "selected");
  });

  // Ctrl-click builds a selection without opening anything; it must survive.
  it("keeps a Ctrl-click selection while the current node stays put", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(row(/Inbox triage/));
    await user.keyboard("{Control>}");
    await user.click(row(/Sweep node A/));
    await user.keyboard("{/Control}");

    expect(row(/Inbox triage/)).toHaveClass("selected");
    expect(row(/Sweep node A/)).toHaveClass("selected");
  });

  // Closing the node leaves nothing current, so nothing stays outlined.
  it("clears when no node is current", async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(row(/Inbox triage/));
    act(() => useApp.setState({ activeTab: null }));

    expect(row(/Inbox triage/)).not.toHaveClass("selected");
  });
});
