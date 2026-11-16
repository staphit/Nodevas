import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translate } from "../../i18n";
import { configurePreferenceStorage, readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { Sidebar } from "../Sidebar";

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
        { id: "node-0001", title: "Inbox triage", assignee: "ming" },
        { id: "node-0002", title: "Sweep the board" },
      ],
      edges: [],
      users: [{ id: "ming", name: "Ming" }],
      ui: {},
    },
    statuses: {},
    issues: [],
    trash: [{ file: "20261101-120000-node-0003.md", kind: "node", nodeId: "node-0003", at: "2026-11-01T12:00:00Z" }],
    workspace: "/ws",
    workspaces: [],
    projects: [{ name: "demo", label: "demo", depth: 0, path: "/ws/demo", nodes: 2 }],
    activeProject: "demo",
    activeTab: null,
    openTab: vi.fn(async () => {}),
    nodeFolders: [],
    nodeFolderOf: {},
  });
}

const row = (name: RegExp) => screen.getByRole("button", { name });

// The second line of a row read "node-0001.md · Unassigned": a file name in
// front of everyone, ahead of the one thing the line is for.
describe("explorer node rows", () => {
  beforeEach(() => seed());

  it("say who has the node and nothing else", () => {
    render(<Sidebar />);

    const assigned = row(/Inbox triage/);
    expect(assigned.querySelector(".node-list-assignee")).toHaveTextContent(/^Ming$/);
    const free = row(/Sweep the board/);
    expect(free.querySelector(".node-list-assignee")).toHaveTextContent(/^Unassigned$/);
    for (const button of [assigned, free]) expect(button.textContent).not.toContain(".md");
  });

  // Still there for whoever looks for the file, and for a screen reader as
  // the row's description.
  it("keep the file name in the tooltip", () => {
    render(<Sidebar />);

    const assigned = row(/Inbox triage/);
    expect(assigned.title.split("\n")).toEqual([expect.stringMatching(/^Inbox triage \(/), "File: node-0001.md"]);
    expect(assigned).toHaveAccessibleDescription(expect.stringContaining("File: node-0001.md"));
  });

  it("read the same way in Traditional Chinese", () => {
    render(<Sidebar />);
    act(() => useApp.getState().updateUIPreference("language", "zh-TW"));

    const free = row(/Sweep the board/);
    expect(free.querySelector(".node-list-assignee")?.textContent).toBe(translate("explorer.unassigned", "zh-TW"));
    expect(free.textContent).not.toContain(".md");
    expect(free.title).toContain(translate("explorer.nodeFileTitle", "zh-TW", { file: "node-0002.md" }));
  });

  // A deleted node's title is not kept: its id is all the trash can call it.
  // The tooltip named node-0003.md, a file that is not in the trash: it keeps
  // the file under a time-stamped name.
  it("name a deleted node by its id, with the trashed file's name in the tooltip", () => {
    render(<Sidebar />);

    const deleted = screen.getByText("node-0003");
    expect(deleted).toHaveAttribute("title", "File: 20261101-120000-node-0003.md");
    expect(deleted.closest("li")?.textContent).not.toContain(".md");
  });

  it("fall back to the node's own file name when the trash gives none", () => {
    useApp.setState({ trash: [{ file: "", kind: "node", nodeId: "node-0004", at: "2026-11-01T12:00:00Z" }] });
    render(<Sidebar />);

    expect(screen.getByText("node-0004")).toHaveAttribute("title", "File: node-0004.md");
  });
});
