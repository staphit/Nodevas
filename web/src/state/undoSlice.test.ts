import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import { clearUndo, pushUndo, undoDepth } from "./undo";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      deleteNode: vi.fn(),
      restoreTrash: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      putGraph: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };

function baseGraph(): Graph {
  return { version: 1, nodes: [{ id: "a" }], edges: [], ui: {} };
}

beforeEach(() => {
  clearUndo();
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getGraph).mockResolvedValue({
    graph: baseGraph(),
    rev: "rev-loaded",
    statuses: {},
    issues: null,
  });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  useApp.setState({
    graph: baseGraph(),
    graphRev: "rev-1",
    tabs: [],
    activeTab: null,
    canUndo: false,
    undoLabel: null,
    canRedo: false,
    redoLabel: null,
    error: null,
  });
});

describe("undoLast with an empty stack", () => {
  it("resolves false and touches nothing", async () => {
    await expect(useApp.getState().undoLast()).resolves.toBe(false);
    expect(api.deleteNode).not.toHaveBeenCalled();
    expect(api.putGraph).not.toHaveBeenCalled();
  });
});

describe("create-node undo", () => {
  it("deletes the created node, closes its tab, and reloads", async () => {
    vi.mocked(api.deleteNode).mockResolvedValue({ ok: true, trashFile: "" });
    useApp.setState({ tabs: [{ id: "new-1", pinned: false, content: "", rev: "r", dirty: false, loaded: true, diskChanged: false, draftPending: null, conflict: null }], activeTab: "new-1" });
    pushUndo({ kind: "create-node", ids: ["new-1"] });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    expect(api.deleteNode).toHaveBeenCalledWith("new-1");
    expect(useApp.getState().tabs.find((t) => t.id === "new-1")).toBeUndefined();
    expect(undoDepth()).toBe(0);
  });
});

describe("delete-node undo", () => {
  it("restores the trashed files, then re-PUTs the prior graph against the freshest rev", async () => {
    vi.mocked(api.restoreTrash).mockResolvedValue({ ok: true, id: "a" });
    vi.mocked(api.getGraph).mockResolvedValue({
      graph: baseGraph(),
      rev: "rev-fresh",
      statuses: {},
      issues: null,
    });
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-restored", issues: null });
    const restoredGraph = baseGraph();
    restoredGraph.nodes = [{ id: "a" }, { id: "b" }];
    pushUndo({ kind: "delete-node", graph: restoredGraph, trashFiles: ["trash/b.md"] });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    expect(api.restoreTrash).toHaveBeenCalledWith("trash/b.md");
    // The write must target the revision just read back, not the stale one
    // sitting in the store before the undo started.
    expect(api.putGraph).toHaveBeenCalledWith(
      expect.objectContaining({ nodes: [{ id: "a" }, { id: "b" }] }),
      "rev-fresh",
    );
    // loadAll() runs right after and re-reads from the (mocked) server, so the
    // assertion that matters is what undo actually submitted, above.
    expect(undoDepth()).toBe(0);
  });

  it("skips restoreTrash when nothing was trashed but still re-PUTs the graph", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    pushUndo({ kind: "delete-node", graph: baseGraph(), trashFiles: [] });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    expect(api.restoreTrash).not.toHaveBeenCalled();
    expect(api.putGraph).toHaveBeenCalled();
  });

  // README rule 4: a failed revert puts the entry back so the next Ctrl/⌘+Z
  // retries the same step. Once the trash files are already restored, retrying
  // the *original* delete-node entry would call restoreTrash a second time on
  // files that no longer exist there — so the code swaps in a plain "graph"
  // retry entry that skips straight to the PUT on the next attempt.
  it("degrades the retry entry to a graph write once the trash restore already landed", async () => {
    vi.mocked(api.restoreTrash).mockResolvedValue({ ok: true, id: "b" });
    vi.mocked(api.putGraph).mockRejectedValueOnce(new Error("磁碟已滿"));
    const restoredGraph = baseGraph();
    restoredGraph.nodes = [{ id: "a" }, { id: "b" }];
    pushUndo({
      kind: "delete-node",
      graph: restoredGraph,
      trashFiles: ["trash/b.md"],
      label: "刪除 1 個節點",
    });

    await expect(useApp.getState().undoLast()).rejects.toThrow("磁碟已滿");
    expect(api.restoreTrash).toHaveBeenCalledTimes(1);
    expect(useApp.getState().canUndo).toBe(true);
    // The label survives the downgrade to a graph-kind entry.
    expect(useApp.getState().undoLabel).toBe("刪除 1 個節點");

    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-3", issues: null });
    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    // Retried as a graph write: the already-restored trash file is not touched again.
    expect(api.restoreTrash).toHaveBeenCalledTimes(1);
    expect(undoDepth()).toBe(0);
  });
});

describe("redo", () => {
  it("resolves false on an empty redo stack", async () => {
    await expect(useApp.getState().redoLast()).resolves.toBe(false);
    expect(api.putGraph).not.toHaveBeenCalled();
  });

  // Redo re-applies the state undo stepped away from: the entry it pushes is
  // built from what was on screen *before* the revert, not from the entry the
  // revert consumed.
  it("re-applies the graph that undo stepped away from", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    const previous: Graph = { version: 1, nodes: [], edges: [], ui: {} };
    pushUndo({ kind: "graph", graph: previous, label: "移動節點" });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);
    expect(api.putGraph).toHaveBeenLastCalledWith(
      expect.objectContaining({ nodes: [] }),
      "rev-1",
    );
    expect(useApp.getState().canUndo).toBe(false);
    expect(useApp.getState().canRedo).toBe(true);
    expect(useApp.getState().redoLabel).toBe("移動節點");

    await expect(useApp.getState().redoLast()).resolves.toBe(true);

    expect(api.putGraph).toHaveBeenLastCalledWith(
      expect.objectContaining({ nodes: [{ id: "a" }] }),
      expect.anything(),
    );
    // The step is back on the undo stack, so it can be reverted again.
    expect(useApp.getState().canRedo).toBe(false);
    expect(useApp.getState().canUndo).toBe(true);
    expect(useApp.getState().undoLabel).toBe("移動節點");
  });

  // Undoing a creation deletes the node into the trash; redoing that has to be
  // the inverse gesture — restore it — not a second create with a new id.
  it("restores from the trash when redoing an undone node creation", async () => {
    vi.mocked(api.deleteNode).mockResolvedValue({ ok: true, trashFile: "trash/new-1.md" });
    vi.mocked(api.restoreTrash).mockResolvedValue({ ok: true, id: "new-1" });
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    pushUndo({ kind: "create-node", ids: ["new-1"] });

    await expect(useApp.getState().undoLast()).resolves.toBe(true);
    expect(api.deleteNode).toHaveBeenCalledWith("new-1");

    await expect(useApp.getState().redoLast()).resolves.toBe(true);

    expect(api.restoreTrash).toHaveBeenCalledWith("trash/new-1.md");
    // And the undo stack now holds the creation again, one gesture from gone.
    expect(useApp.getState().canUndo).toBe(true);
    expect(useApp.getState().undoLabel).toBe("新增節點");
  });

  // README rule (redo) 2: an edit after an undo branches the history. The redo
  // entry describes the abandoned branch, so it must not survive.
  it("drops the redo stack when a fresh edit is recorded", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    pushUndo({ kind: "graph", graph: { version: 1, nodes: [], edges: [], ui: {} } });
    await useApp.getState().undoLast();
    expect(useApp.getState().canRedo).toBe(true);

    pushUndo({ kind: "graph", graph: baseGraph(), label: "改標題" });

    expect(useApp.getState().canRedo).toBe(false);
    expect(useApp.getState().redoLabel).toBeNull();
    await expect(useApp.getState().redoLast()).resolves.toBe(false);
  });

  // Same retry rule as undo: a failed redo keeps its entry, so the next
  // Ctrl/⌘+Shift+Z tries the same step rather than skipping ahead.
  it("puts the entry back on the redo stack when the write fails", async () => {
    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    pushUndo({ kind: "graph", graph: { version: 1, nodes: [], edges: [], ui: {} } });
    await useApp.getState().undoLast();

    vi.mocked(api.putGraph).mockRejectedValueOnce(new Error("磁碟已滿"));
    await expect(useApp.getState().redoLast()).rejects.toThrow("磁碟已滿");
    expect(useApp.getState().canRedo).toBe(true);

    await expect(useApp.getState().redoLast()).resolves.toBe(true);
    expect(useApp.getState().canRedo).toBe(false);
  });
});

describe("undo entries queue behind the graph write lane", () => {
  // undoLast chains onto `queues.graphSave` rather than firing immediately, so
  // a write already queued there finishes before the revert starts — the two
  // must never race for the same `baseRev`.
  it("waits for a write already queued on queues.graphSave before reverting", async () => {
    const { queues } = await import("./internals");
    let releasePending: (() => void) | null = null;
    const order: string[] = [];
    const pending = queues.graphSave.then(async () => {
      order.push("pending-start");
      await new Promise<void>((resolve) => {
        releasePending = resolve;
      });
      order.push("pending-done");
    });
    queues.graphSave = pending.then(() => undefined);

    vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
    pushUndo({ kind: "delete-node", graph: baseGraph(), trashFiles: [] });

    const undo = useApp.getState().undoLast();
    await vi.waitFor(() => expect(order).toContain("pending-start"));
    expect(order).not.toContain("pending-done");
    expect(api.putGraph).not.toHaveBeenCalled();

    releasePending!();
    await undo;

    expect(order).toEqual(["pending-start", "pending-done"]);
    expect(api.putGraph).toHaveBeenCalled();
  });
});
