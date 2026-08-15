import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useApp } from "../store";
import type { Graph } from "../types";
import { Sidebar, SidebarBackdrop } from "./Sidebar";

/**
 * jsdom's matchMedia always reports "no match" and has no listener plumbing,
 * so the breakpoint is faked here — same stand-in TopbarOverflow.test.tsx
 * uses, because both components hang their behaviour on the same query.
 */
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

describe("SidebarBackdrop", () => {
  afterEach(() => vi.unstubAllGlobals());

  // A wide window shows the explorer beside the board, not over it, so there
  // is nothing to dim and nothing a backdrop tap could sensibly mean.
  it("renders nothing on a wide viewport", () => {
    mockViewport(false);
    const { container } = render(<SidebarBackdrop open onClose={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing while the explorer is collapsed", () => {
    mockViewport(true);
    const { container } = render(
      <SidebarBackdrop open={false} onClose={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("appears when the explorer overlays a narrow viewport", () => {
    mockViewport(true);
    const { container } = render(<SidebarBackdrop open onClose={vi.fn()} />);
    expect(container.querySelector(".sidebar-backdrop")).toBeInTheDocument();
  });

  it("closes the explorer when tapped", async () => {
    mockViewport(true);
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { container } = render(<SidebarBackdrop open onClose={onClose} />);

    await user.click(container.querySelector(".sidebar-backdrop")!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

function graph(nodes: Graph["nodes"]): Graph {
  return { version: 1, nodes, edges: [], ui: {} };
}

/** Enough store state for the tree to show one project with one node. */
function seedStore(openTab: (id: string) => Promise<void>) {
  useApp.setState({
    graph: graph([{ id: "a", title: "設計稿" }]),
    statuses: {},
    issues: [],
    trash: [],
    workspace: "H:/ws",
    workspaces: [],
    projects: [
      { name: "demo", label: "demo", depth: 0, path: "H:/ws/demo", nodes: 1 },
    ],
    activeProject: "demo",
    activeTab: null,
    openTab,
    nodeFolders: [],
    nodeFolderOf: {},
  });
}

describe("Sidebar on a phone", () => {
  beforeEach(() => {
    // The store outlives the test file; the preference must start from the
    // wide-window default rather than whatever the previous test left.
    useApp.getState().updateUIPreference("explorerCollapsed", false);
  });
  afterEach(() => vi.unstubAllGlobals());

  // Opening a node opens the bottom-sheet drawer over the overlay, so the
  // sidebar left behind is dead weight — it collapses as part of the gesture.
  it("collapses the explorer after opening a node", async () => {
    mockViewport(true);
    const openTab = vi.fn(async () => {});
    seedStore(openTab);
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(screen.getByRole("button", { name: /設計稿/ }));

    expect(openTab).toHaveBeenCalledWith("a");
    await waitFor(() =>
      expect(useApp.getState().preferences.explorerCollapsed).toBe(true),
    );
  });

  it("keeps the explorer open on a wide viewport", async () => {
    mockViewport(false);
    const openTab = vi.fn(async () => {});
    seedStore(openTab);
    const user = userEvent.setup();
    render(<Sidebar />);

    await user.click(screen.getByRole("button", { name: /設計稿/ }));

    await waitFor(() => expect(openTab).toHaveBeenCalledWith("a"));
    expect(useApp.getState().preferences.explorerCollapsed).toBe(false);
  });
});
