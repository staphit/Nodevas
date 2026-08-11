import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { RunState } from "../types";
import { invalidateGenerations } from "./internals";
import { clearUndo } from "./undo";

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      setStatus: vi.fn(),
      moveEvent: vi.fn(),
      getState: vi.fn(),
      getGraph: vi.fn(),
      getTrash: vi.fn(),
    },
  };
});

function runState(partial: Partial<RunState> = {}): RunState {
  return { nodes: {}, history: [], ...partial };
}

beforeEach(() => {
  clearUndo();
  useApp.setState({
    graph: null,
    runState: runState(),
    statuses: {},
    operations: {},
    lastOperation: null,
    canUndo: false,
    undoLabel: null,
    canRedo: false,
    redoLabel: null,
    error: null,
  });
});

describe("setLifecycleStatus", () => {
  it("mirrors the journal reply and reports saved", async () => {
    const next = runState({ nodes: { a: { status: "done" } } });
    vi.mocked(api.setStatus).mockResolvedValue({
      ok: true,
      state: next,
      statuses: { a: "done" },
    });

    const result = await useApp.getState().setLifecycleStatus("a", "done", "完成");

    expect(result.ok).toBe(true);
    expect(api.setStatus).toHaveBeenCalledWith("a", "done", "完成");
    expect(useApp.getState().statuses).toEqual({ a: "done" });
    expect(useApp.getState().operations["lifecycle:a"]).toMatchObject({
      status: "saved",
      operation: "lifecycle.set",
    });
  });

  it("reports the failure by value instead of throwing", async () => {
    vi.mocked(api.setStatus).mockRejectedValue(new Error("journal locked"));

    const result = await useApp.getState().setLifecycleStatus("a", "done");

    expect(result.ok === false && result.message).toBe("journal locked");
    expect(useApp.getState().operations["lifecycle:a"]).toMatchObject({
      status: "error",
    });
  });

  it("keeps the throwing wrapper working for existing callers", async () => {
    vi.mocked(api.setStatus).mockRejectedValue(new Error("journal locked"));
    await expect(useApp.getState().setStatus("a", "done")).rejects.toThrow("journal locked");
  });
});

describe("journal undo", () => {
  it("reverts by appending the previous status, never by deleting history", async () => {
    useApp.setState({ runState: runState({ nodes: { a: { status: "started" } } }) });
    vi.mocked(api.setStatus).mockResolvedValue({
      ok: true,
      state: runState({ nodes: { a: { status: "done" } } }),
      statuses: { a: "done" },
    });

    await useApp.getState().setLifecycleStatus("a", "done");
    expect(useApp.getState().canUndo).toBe(true);
    expect(useApp.getState().undoLabel).toBe("實際狀態變更");

    await expect(useApp.getState().undoLast()).resolves.toBe(true);

    expect(api.setStatus).toHaveBeenLastCalledWith(
      "a",
      "started",
      "撤銷：回到先前的實際狀態",
    );
    // The compensating write must not push another undo step.
    expect(useApp.getState().canUndo).toBe(false);
  });

  // Redo is another append, not a rewind: it puts back the status the undo
  // compensated away, and the note says which gesture wrote it.
  it("redoes by appending the status the undo stepped away from", async () => {
    useApp.setState({ runState: runState({ nodes: { a: { status: "started" } } }) });
    vi.mocked(api.setStatus).mockImplementation(async (_id, status) => ({
      ok: true,
      state: runState({ nodes: { a: { status } } }),
      statuses: { a: status },
    }));

    await useApp.getState().setLifecycleStatus("a", "done");
    await useApp.getState().undoLast();
    expect(useApp.getState().canRedo).toBe(true);
    expect(useApp.getState().redoLabel).toBe("實際狀態變更");

    await expect(useApp.getState().redoLast()).resolves.toBe(true);

    expect(api.setStatus).toHaveBeenLastCalledWith("a", "done", "重做：重新套用實際狀態");
    expect(useApp.getState().canRedo).toBe(false);
    expect(useApp.getState().canUndo).toBe(true);
  });

  it("does not offer undo when the previous state was only derived", async () => {
    // No entry in runState.nodes: the node sat in a computed ready/locked state,
    // so there is no explicit event to restore.
    vi.mocked(api.setStatus).mockResolvedValue({
      ok: true,
      state: runState({ nodes: { a: { status: "done" } } }),
      statuses: { a: "done" },
    });

    await useApp.getState().setLifecycleStatus("a", "done");

    expect(useApp.getState().canUndo).toBe(false);
  });

  it("reverts a moved event back to its original timestamp", async () => {
    useApp.setState({
      runState: runState({
        history: [
          { id: "ev-1", t: "2026-08-01T00:00:00Z", event: "status", node: "a", to: "done" },
        ],
      }),
    });
    vi.mocked(api.moveEvent).mockResolvedValue({
      ok: true,
      state: runState({
        history: [
          { id: "ev-1", t: "2026-08-05T00:00:00Z", event: "status", node: "a", to: "done" },
        ],
      }),
      statuses: { a: "done" },
    });

    await useApp.getState().moveActualEvent("ev-1", "2026-08-05T00:00:00Z");
    expect(useApp.getState().operations["lifecycle:a"]).toMatchObject({
      status: "saved",
      operation: "lifecycle.move",
    });
    expect(useApp.getState().undoLabel).toBe("實際時間調整");

    await useApp.getState().undoLast();

    expect(api.moveEvent).toHaveBeenLastCalledWith(
      "ev-1",
      "2026-08-01T00:00:00Z",
      "撤銷：回到先前的時間",
    );
  });
});

describe("refreshState", () => {
  // A graph write, a lifecycle change and the server's `state-changed` echo all
  // ask for this within a few milliseconds of each other; that is one read.
  it("shares one read between callers that ask at the same moment", async () => {
    vi.mocked(api.getState).mockResolvedValue({ state: runState(), statuses: { a: "done" } });

    await Promise.all([
      useApp.getState().refreshState(),
      useApp.getState().refreshState(),
      useApp.getState().refreshState(),
    ]);

    expect(api.getState).toHaveBeenCalledTimes(1);
    expect(useApp.getState().statuses).toEqual({ a: "done" });
  });

  it("ignores a reply that belongs to the project that was open before", async () => {
    let resolveStale: ((value: Awaited<ReturnType<typeof api.getState>>) => void) | null =
      null;
    vi.mocked(api.getState).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveStale = resolve;
        }),
    );

    const stale = useApp.getState().refreshState();
    await vi.waitFor(() => expect(api.getState).toHaveBeenCalled());

    // A project switch invalidates everything still in flight: those statuses
    // describe a project the window is no longer showing.
    invalidateGenerations();
    resolveStale!({ state: runState(), statuses: { a: "failed" } });
    await stale;

    expect(useApp.getState().statuses).toEqual({});
  });
});
