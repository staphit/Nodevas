import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { pushUndo } from "./undo";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      openProject: vi.fn(),
      removeWorkspace: vi.fn(),
      putDraft: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      getProjects: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };
const NEXT_GRAPH: Graph = { version: 1, nodes: [{ id: "b" }], edges: [], ui: {} };

function dirtyTab(id: string) {
  return {
    id,
    pinned: false,
    content: "未存的內容",
    rev: "rev-1",
    dirty: true,
    loaded: true,
    diskChanged: false,
    draftPending: null,
    conflict: null,
  };
}

beforeEach(() => {
  vi.mocked(api.openProject).mockResolvedValue({ root: "/tmp/p2", active: "p2" });
  vi.mocked(api.removeWorkspace).mockResolvedValue({
    ok: true,
    workspace: "/tmp",
  });
  vi.mocked(api.putDraft).mockResolvedValue({ ok: true });
  vi.mocked(api.getGraph).mockResolvedValue({
    graph: NEXT_GRAPH,
    rev: "rev-2",
    statuses: {},
    issues: null,
  });
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  vi.mocked(api.getProjects).mockResolvedValue({
    workspace: "/tmp",
    workspaces: [],
    active: "p2",
    projects: [],
  });
  useApp.setState({
    graph: { version: 1, nodes: [{ id: "a" }], edges: [], ui: {} },
    graphRev: "rev-1",
    tabs: [dirtyTab("a")],
    activeTab: "a",
    workspace: "/tmp/added",
    workspaces: [
      { path: "/tmp", label: "tmp", active: false, projects: 1 },
      { path: "/tmp/added", label: "added", active: true, projects: 1 },
    ],
    activeProject: "p1",
    operations: { "node:a": { status: "error", message: "舊專案的錯誤" } },
    lastOperation: null,
    canUndo: false,
    undoLabel: null,
    canRedo: false,
    redoLabel: null,
  });
});

describe("removeWorkspace", () => {
  it("parks unsaved text before removing the active workspace", async () => {
    await useApp.getState().removeWorkspace("/tmp/added");

    expect(api.putDraft).toHaveBeenCalledWith("a", "未存的內容");
    expect(api.putDraft).toHaveBeenCalledBefore(
      vi.mocked(api.removeWorkspace),
    );
    expect(useApp.getState()).toMatchObject({
      workspace: "/tmp",
      tabs: [],
      activeTab: null,
    });
  });
});

describe("switchProject", () => {
  it("parks unsaved text as a draft before leaving", async () => {
    await useApp.getState().switchProject("p2");
    expect(api.putDraft).toHaveBeenCalledWith("a", "未存的內容");
  });

  it("drops tabs, badges and undo history belonging to the old project", async () => {
    pushUndo({ kind: "create-node", ids: ["a"] });
    expect(useApp.getState().canUndo).toBe(true);

    await useApp.getState().switchProject("p2");

    const state = useApp.getState();
    expect(state.tabs).toEqual([]);
    expect(state.activeTab).toBeNull();
    expect(state.activeProject).toBe("p2");
    expect(state.operations["node:a"]).toBeUndefined();
    expect(state.canUndo).toBe(false);
    expect(state.graph).toEqual(NEXT_GRAPH);
  });

  it("reports the switch itself through the workspace scope", async () => {
    await useApp.getState().switchProject("p2");
    expect(useApp.getState().operations.workspace).toMatchObject({
      status: "saved",
      operation: "workspace.switchProject",
    });
  });

  it("surfaces a failed switch as an error badge and rethrows", async () => {
    vi.mocked(api.openProject).mockRejectedValue(new Error("專案不存在"));

    await expect(useApp.getState().switchProject("ghost")).rejects.toThrow("專案不存在");

    expect(useApp.getState().operations.workspace).toMatchObject({
      status: "error",
      message: "專案不存在",
    });
    // The old project is still loaded: nothing was cleared on the way out.
    expect(useApp.getState().activeProject).toBe("p1");
    expect(useApp.getState().tabs).toHaveLength(1);
  });
});
