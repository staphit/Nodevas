import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { explorerApi } from "../api/explorer";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";
import ExplorerRail from "./ExplorerRail";
import ExplorerShell from "./ExplorerShell";

vi.mock("../components/Sidebar", async (original) => ({
  ...(await original<typeof import("../components/Sidebar")>()),
  Sidebar: () => <div data-testid="node-sidebar">nodes</div>,
}));

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
    map,
  };
}

let storage: ReturnType<typeof fakeStorage>;

beforeEach(() => {
  storage = fakeStorage();
  configurePreferenceStorage(storage);
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  vi.spyOn(explorerApi, "workspaceFiles").mockResolvedValue({ entries: [], hidden: 0, truncated: false });
  vi.spyOn(explorerApi, "repos").mockResolvedValue([]);
  vi.spyOn(explorerApi, "workspaceGit").mockResolvedValue({ backend: "git", branch: "main", pending: [], commits: [] });
  vi.spyOn(explorerApi, "agents").mockResolvedValue({ live: [], external: [], recent: [] });
});
afterEach(() => vi.restoreAllMocks());

// W-09: the rail's four tabs.
describe("Explorer rail", () => {
  it("names its four tabs and marks the current one", async () => {
    const onTab = vi.fn();
    render(<ExplorerRail tab="files" onTab={onTab} />);
    const rail = screen.getByRole("group", { name: "Sidebar tabs" });
    const tabs = within(rail).getAllByRole("button");
    expect(tabs.map((tab) => tab.getAttribute("aria-label"))).toEqual(["Nodes", "Files", "Git", "Agent"]);
    expect(within(rail).getByRole("button", { name: "Files" })).toHaveAttribute("aria-pressed", "true");
    expect(within(rail).getByRole("button", { name: "Nodes" })).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(within(rail).getByRole("button", { name: "Agent" }));
    expect(onTab).toHaveBeenCalledWith("agent");
  });

  it("shows the node sidebar on Nodes and remembers the tab", async () => {
    render(<ExplorerShell />);
    expect(screen.getByTestId("node-sidebar")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(screen.queryByTestId("node-sidebar")).not.toBeInTheDocument();
    expect(storage.map.get("vised-explorer-tab")).toContain("git");
    expect((useApp.getState().preferences as unknown as Record<string, unknown>).explorerTab).toBe("git");
  });
});

/** jsdom's matchMedia reports no match; the phone breakpoint is faked here. */
function mockViewport(narrow: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: narrow,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
}

const collapsed = () =>
  (useApp.getState().preferences as unknown as Record<string, unknown>).explorerCollapsed;

// On a phone the Files, Git and Agent panels overlay the board, and only the
// Nodes tab had a way to fold the explorer away.
describe("Folding the explorer from any tab", () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each(["Files", "Git", "Agent"])("%s has a collapse control", async (name) => {
    render(<ExplorerShell />);
    await userEvent.click(screen.getByRole("button", { name }));
    const collapse = screen.getByRole("button", { name: "Collapse project explorer" });
    expect(collapse).toHaveAttribute("aria-controls", "project-explorer");
    await userEvent.click(collapse);
    expect(collapsed()).toBe(true);
  });

  it("leaves Nodes to its own collapse control", () => {
    render(<ExplorerShell />);
    const rail = screen.getByRole("group", { name: "Sidebar tabs" });
    expect(within(rail).queryByRole("button", { name: "Collapse project explorer" })).not.toBeInTheDocument();
  });

  it("closes the phone overlay when the open tab is tapped again", async () => {
    mockViewport(true);
    render(<ExplorerShell />);
    await userEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(collapsed()).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(collapsed()).toBe(true);
  });

  it("keeps the open tab open on a wide window, where the panel sits beside the board", async () => {
    mockViewport(false);
    render(<ExplorerShell />);
    await userEvent.click(screen.getByRole("button", { name: "Git" }));
    await userEvent.click(screen.getByRole("button", { name: "Git" }));
    expect(collapsed()).toBe(false);
  });

  it("closes the phone overlay on Escape", async () => {
    mockViewport(true);
    render(<ExplorerShell />);
    await userEvent.click(screen.getByRole("button", { name: "Files" }));
    await userEvent.keyboard("{Escape}");
    expect(collapsed()).toBe(true);
  });
});
