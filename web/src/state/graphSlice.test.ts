import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ConflictError } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { invalidateGenerations } from "./internals";
import { clearUndo } from "./undo";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      putGraph: vi.fn(),
      graphOps: vi.fn(),
      getTrash: vi.fn(),
      getProjects: vi.fn(),
      checkDSL: vi.fn(),
      createNode: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

function baseGraph(): Graph {
  return {
    version: 1,
    nodes: [{ id: "a", title: "A" }],
    edges: [],
    ui: {},
  };
}

function resetStore(graph: Graph | null = baseGraph()) {
  clearUndo();
  useApp.setState({
    graph,
    graphRev: "rev-1",
    issues: [],
    error: null,
    operations: {},
    lastOperation: null,
    runState: null,
    statuses: {},
    canUndo: false,
    undoLabel: null,
    canRedo: false,
    redoLabel: null,
  });
}

/** A POST /api/graph/ops success returning `graph` as the server's merged state. */
function opsResponse(graph: Graph, rev = "rev-2") {
  return { ok: true, graph, rev, statuses: {}, issues: null };
}

beforeEach(() => {
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getGraph).mockResolvedValue({
    graph: baseGraph(),
    rev: "rev-1",
    statuses: {},
    issues: null,
  });
  vi.mocked(api.graphOps).mockResolvedValue(opsResponse(baseGraph()));
  resetStore();
});

describe("runGraphCommand", () => {
  it("routes a node-field edit through ops and adopts the server graph", async () => {
    const merged = baseGraph();
    merged.nodes![0].title = "改過";
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(merged));

    const result = await useApp.getState().updateNodeMetadata("a", { title: "改過" });

    expect(result.ok).toBe(true);
    // A node field edit must not take the whole-file lock.
    expect(api.putGraph).not.toHaveBeenCalled();
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      { kind: "node-metadata", nodeId: "a", title: "改過" },
    ]);
    // The graph shown is the server's merged result, not the local optimistic copy.
    expect(useApp.getState().graph!.nodes![0].title).toBe("改過");
    expect(useApp.getState().graphRev).toBe("rev-2");
    expect(useApp.getState().operations["node:a"]).toMatchObject({
      status: "saved",
      operation: "node.updateMetadata",
    });
    expect(useApp.getState().canUndo).toBe(true);
    expect(useApp.getState().undoLabel).toBe("修改節點資料");
  });

  it("routes a write-access edit through ops", async () => {
    const merged = baseGraph();
    merged.nodes![0].writeAccess = "human-only";
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(merged));

    const result = await useApp
      .getState()
      .updateNodeMetadata("a", { writeAccess: "human-only" });

    expect(result.ok).toBe(true);
    expect(api.putGraph).not.toHaveBeenCalled();
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      { kind: "node-metadata", nodeId: "a", writeAccess: "human-only" },
    ]);
    expect(useApp.getState().graph!.nodes![0].writeAccess).toBe("human-only");
  });

  it("clears write access with an empty value on the op", async () => {
    const restricted = baseGraph();
    restricted.nodes![0].writeAccess = "worker";
    resetStore(restricted);
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(baseGraph()));

    const result = await useApp.getState().updateNodeMetadata("a", { writeAccess: "" });

    expect(result.ok).toBe(true);
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      { kind: "node-metadata", nodeId: "a", writeAccess: "" },
    ]);
    expect(useApp.getState().graph!.nodes![0].writeAccess).toBeUndefined();
  });

  it("rejects a write-access value outside the hierarchy before the network", async () => {
    const result = await useApp
      .getState()
      .updateNodeMetadata("a", { writeAccess: "admin" });

    expect(result.ok).toBe(false);
    expect(api.graphOps).not.toHaveBeenCalled();
    expect(api.putGraph).not.toHaveBeenCalled();
  });

  it("keeps whole-file writes on PUT /api/graph for non-op-able commands", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });

    const result = await useApp.getState().updateWorkflowDefinition({
      type: "workflow.addMilestoneType",
      label: "內審",
    });

    expect(result.ok).toBe(true);
    expect(api.putGraph).toHaveBeenCalledTimes(1);
    expect(api.graphOps).not.toHaveBeenCalled();
  });

  it("routes a canvas drag through ops, one move per node", async () => {
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(baseGraph()));

    await useApp.getState().updateCanvasLayout({
      type: "canvas.moveNodes",
      positions: { a: { x: 10, y: 20 } },
    });

    expect(api.putGraph).not.toHaveBeenCalled();
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      { kind: "move", nodeId: "a", x: 10, y: 20 },
    ]);
  });

  it("keeps a required edge visible until the server removes its condition atomically", async () => {
    const before: Graph = {
      version: 1,
      nodes: [
        { id: "a", title: "A" },
        { id: "b", title: "B", requires: "a" },
      ],
      edges: [{ from: "a", to: "b" }],
      ui: {},
    };
    resetStore(before);
    let resolveOps: ((value: ReturnType<typeof opsResponse>) => void) | undefined;
    vi.mocked(api.graphOps).mockImplementationOnce(
      () => new Promise((resolve) => { resolveOps = resolve; }),
    );

    const pending = useApp.getState().updateCanvasLayout({
      type: "canvas.removeEdge",
      from: "a",
      to: "b",
    });
    await vi.waitFor(() => expect(api.graphOps).toHaveBeenCalled());
    // The edge-only reducer draft is not exposed while Go has not yet pruned
    // `requires`; UI analysis therefore never observes split-brain state.
    expect(useApp.getState().graph).toEqual(before);

    const merged = structuredClone(before);
    merged.edges = [];
    merged.nodes![1].requires = undefined;
    resolveOps!(opsResponse(merged));
    await expect(pending).resolves.toMatchObject({ ok: true });
    expect(vi.mocked(api.graphOps).mock.calls[0][0]).toEqual([
      { kind: "remove-edge", from: "a", to: "b" },
    ]);
    expect(useApp.getState().graph).toEqual(merged);
  });

  it("rolls the optimistic update back when the write fails", async () => {
    vi.mocked(api.graphOps).mockRejectedValue(new Error("disk full"));

    const result = await useApp.getState().updateNodeMetadata("a", { title: "改過" });

    expect(result.ok).toBe(false);
    expect(result.ok === false && result.message).toBe("disk full");
    expect(useApp.getState().graph!.nodes![0].title).toBe("A");
    expect(useApp.getState().operations["node:a"]).toMatchObject({
      status: "error",
      message: "disk full",
    });
    // A write that never landed must not be undoable.
    expect(useApp.getState().canUndo).toBe(false);
  });

  it("reloads and reports a conflict when the disk moved underneath", async () => {
    vi.mocked(api.graphOps).mockRejectedValue(new ConflictError("rev-9", "..."));
    const disk = baseGraph();
    disk.nodes![0].title = "別人改的";
    vi.mocked(api.getGraph).mockResolvedValue({
      graph: disk,
      rev: "rev-9",
      statuses: {},
      issues: null,
    });

    const result = await useApp.getState().updateNodeMetadata("a", { title: "我改的" });

    expect(result.ok === false && result.status).toBe("conflict");
    expect(useApp.getState().graph!.nodes![0].title).toBe("別人改的");
    expect(useApp.getState().graphRev).toBe("rev-9");
    expect(useApp.getState().operations["node:a"]).toMatchObject({ status: "conflict" });
    expect(useApp.getState().canUndo).toBe(false);
  });

  it("fails validation before touching the network", async () => {
    const result = await useApp.getState().upsertPlanMilestone("a", {
      type: "done",
      date: "八月五號",
    });

    expect(result.ok).toBe(false);
    expect(api.putGraph).not.toHaveBeenCalled();
    expect(useApp.getState().operations["plan:a"]).toMatchObject({ status: "error" });
  });

  it("scopes the badge by domain so panels do not share one spinner", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });

    await useApp.getState().updateWorkflowDefinition({
      type: "workflow.addMilestoneType",
      label: "內審",
    });
    await useApp.getState().updateCanvasLayout({
      type: "canvas.setTimelineOrder",
      order: ["a"],
    });

    const operations = useApp.getState().operations;
    expect(operations.workflow).toMatchObject({ status: "saved" });
    expect(operations.canvas).toMatchObject({ status: "saved" });
  });

  it("refuses to run before a project is loaded", async () => {
    resetStore(null);
    const result = await useApp.getState().updateNodeMetadata("a", { title: "x" });
    expect(result.ok).toBe(false);
    expect(api.putGraph).not.toHaveBeenCalled();
  });
});

describe("createNode", () => {
  it("carries writeAccess in the create payload", async () => {
    vi.mocked(api.createNode).mockResolvedValue({ ok: true, id: "n1" });

    await useApp
      .getState()
      .createNode(
        { title: "限制節點", kind: "task", writeAccess: "orchestrator" },
        { openDocument: false },
      );

    expect(api.createNode).toHaveBeenCalledWith({
      title: "限制節點",
      kind: "task",
      writeAccess: "orchestrator",
    });
  });
});

describe("undo", () => {
  it("restores the previous graph and empties the stack", async () => {
    // The edit lands through ops; undo replays the whole prior graph via PUT.
    const merged = baseGraph();
    merged.nodes![0].title = "改過";
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(merged));
    await useApp.getState().updateNodeMetadata("a", { title: "改過" });

    vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
    const restored = baseGraph();
    vi.mocked(api.getGraph).mockResolvedValue({
      graph: restored,
      rev: "rev-3",
      statuses: {},
      issues: null,
    });
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-3", issues: null });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    const submitted = vi.mocked(api.putGraph).mock.calls.at(-1)![0];
    expect(submitted.nodes![0].title).toBe("A");
    expect(useApp.getState().canUndo).toBe(false);
  });

  it("keeps the step in the stack when the revert itself fails", async () => {
    const merged = baseGraph();
    merged.nodes![0].title = "改過";
    vi.mocked(api.graphOps).mockResolvedValue(opsResponse(merged));
    await useApp.getState().updateNodeMetadata("a", { title: "改過" });

    vi.mocked(api.putGraph).mockRejectedValueOnce(new Error("還原失敗"));
    await expect(useApp.getState().undoLast()).rejects.toThrow("還原失敗");

    expect(useApp.getState().canUndo).toBe(true);
    expect(useApp.getState().graph!.nodes![0].title).toBe("改過");
  });
});

describe("loadAll", () => {
  // A reload is triggered from several places at once — the mutation itself,
  // the WebSocket echo of it, the effect that re-runs — and every one of those
  // used to be its own round trip.
  it("costs one read of each resource however many callers ask at once", async () => {
    await Promise.all([
      useApp.getState().loadAll(),
      useApp.getState().loadAll(),
      useApp.getState().loadAll(),
    ]);

    expect(api.getGraph).toHaveBeenCalledTimes(1);
    expect(api.getState).toHaveBeenCalledTimes(1);
    expect(useApp.getState().graph!.nodes![0].title).toBe("A");
  });

  it("drops a response that belongs to the project that was open before", async () => {
    let resolveStale: ((value: Awaited<ReturnType<typeof api.getGraph>>) => void) | null =
      null;
    const stale = baseGraph();
    stale.nodes![0].title = "舊專案";

    vi.mocked(api.getGraph).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveStale = resolve;
        }),
    );

    const pending = useApp.getState().loadAll();
    // Wait for the batching window so the read is actually on the wire before
    // the switch happens underneath it.
    await vi.waitFor(() => expect(api.getGraph).toHaveBeenCalled());

    // What a project switch does: everything still in flight belongs to the
    // project that is no longer on screen.
    invalidateGenerations();
    resolveStale!({ graph: stale, rev: "rev-stale", statuses: {}, issues: null });
    await pending;

    expect(useApp.getState().graph!.nodes![0].title).toBe("A");
    expect(useApp.getState().graphRev).toBe("rev-1");
  });
});

describe("transferNodes", () => {
  beforeEach(() => {
    vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
    vi.mocked(api.getProjects).mockResolvedValue({
      workspace: "/w",
      workspaces: [],
      active: "main",
      projects: [],
    });
    useApp.setState({ activeProject: "main", nodeClipboard: null, tabs: [], activeTab: null });
  });

  it("names the project it is showing as the source and reloads afterwards", async () => {
    const transfer = vi.fn().mockResolvedValue({
      ok: true,
      mode: "copy",
      ids: { a: "node-0007" },
      order: ["node-0007"],
    });
    vi.mocked(api).transferNodes = transfer;

    const result = await useApp
      .getState()
      .transferNodes({ ids: ["a"], target: "other", mode: "copy", source: "main" });

    expect(transfer).toHaveBeenCalledWith({
      ids: ["a"],
      target: "other",
      mode: "copy",
      source: "main",
    });
    expect(result.ids.a).toBe("node-0007");
    // Both projects' node counts moved, and either end may be on screen.
    expect(api.getGraph).toHaveBeenCalled();
    expect(api.getProjects).toHaveBeenCalled();
  });

  // Node ids only mean something inside one project, so the clipboard has to
  // remember which project it took them from.
  it("keeps the source project on the clipboard", () => {
    useApp.getState().setNodeClipboard({
      project: "main",
      ids: ["a"],
      labels: ["A"],
      mode: "cut",
    });
    expect(useApp.getState().nodeClipboard).toMatchObject({ project: "main", mode: "cut" });
    useApp.getState().clearNodeClipboard();
    expect(useApp.getState().nodeClipboard).toBeNull();
  });
});
