import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { Graph, RunState } from "../types";
import {
  clearUndo,
  dropBatchUndo,
  observeBatchRevert,
  peekRedo,
  peekUndo,
  pushRedo,
  pushUndo,
  redoDepth,
  undoDepth,
  type BatchRevert,
} from "./undo";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      undoBatch: vi.fn(),
      redoBatch: vi.fn(),
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      putGraph: vi.fn(),
    },
  };
});

const EMPTY_RUN: RunState = { nodes: {}, history: [] };
const graph = (): Graph => ({ version: 1, nodes: [{ id: "a" }], edges: [], ui: {} });

beforeEach(() => {
  clearUndo();
  vi.mocked(api.getState).mockResolvedValue({ state: EMPTY_RUN, statuses: {} });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: graph(), rev: "r", statuses: {}, issues: null });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  useApp.setState({ graph: graph(), graphRev: "r", tabs: [], activeTab: null, error: null });
});

describe("batch undo", () => {
  it("calls the batch undo endpoint, then its redo, and tells the listener both times", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({
      ok: true,
      trashed: ["n1"],
      refused: [{ id: "n2", code: "changed", reason: "edited" }],
    });
    vi.mocked(api.redoBatch).mockResolvedValue({ ok: true, restored: ["n1"], skipped: [], links: 0 });
    const heard: BatchRevert[] = [];
    const stop = observeBatchRevert((revert) => heard.push(revert));
    pushUndo({ kind: "batch", batchId: "b1", ids: ["n1", "n2"], entryId: "e1", conversationId: "c1" });
    expect(useApp.getState().undoLabel).toBe("Copilot draft");

    await expect(useApp.getState().undoLast()).resolves.toBe(true);
    expect(api.undoBatch).toHaveBeenCalledWith("b1");
    expect(undoDepth()).toBe(0);
    expect(peekRedo()).toMatchObject({ kind: "batch", batchId: "b1", undone: true });
    expect(heard[0]).toMatchObject({ direction: "undo", trashed: ["n1"], refused: [{ id: "n2" }] });

    await expect(useApp.getState().redoLast()).resolves.toBe(true);
    expect(api.redoBatch).toHaveBeenCalledWith("b1");
    expect(redoDepth()).toBe(0);
    expect(peekUndo()).toMatchObject({ kind: "batch", batchId: "b1", undone: false });
    expect(heard[1]).toMatchObject({ direction: "redo", restored: ["n1"] });
    stop();
  });

  it("pushes no redo entry when the server kept every node, and keeps the older redo steps", async () => {
    vi.mocked(api.undoBatch).mockResolvedValue({
      ok: true,
      trashed: [],
      refused: [{ id: "n1", code: "claimed", reason: "claimed" }],
    });
    const heard: BatchRevert[] = [];
    const stop = observeBatchRevert((revert) => heard.push(revert));
    pushUndo({ kind: "batch", batchId: "b1", ids: ["n1"], entryId: "e1", conversationId: "c1" });
    pushRedo({ kind: "create-node", ids: ["x"] });
    await expect(useApp.getState().undoLast()).resolves.toBe(true);
    expect(undoDepth()).toBe(0);
    expect(redoDepth()).toBe(1);
    expect(peekRedo()).toMatchObject({ kind: "create-node" });
    expect(heard[0]).toMatchObject({ direction: "undo", trashed: [], refused: [{ id: "n1", code: "claimed" }] });
    stop();
  });

  it("dropBatchUndo takes a batch's step off the stack, wherever it sits", () => {
    pushUndo({ kind: "batch", batchId: "b1", ids: ["n1"] });
    pushUndo({ kind: "create-node", ids: ["x"] });
    dropBatchUndo("b1");
    expect(undoDepth()).toBe(1);
    expect(peekUndo()).toMatchObject({ kind: "create-node" });
  });

  it("puts the entry back when the endpoint fails", async () => {
    vi.mocked(api.undoBatch).mockRejectedValue(new Error("offline"));
    pushUndo({ kind: "batch", batchId: "b1", ids: ["n1"] });
    await expect(useApp.getState().undoLast()).rejects.toThrow("offline");
    expect(undoDepth()).toBe(1);
  });
});
