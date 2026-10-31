import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { useApp } from "../store";
import type { Graph } from "../types";
import { operationScope } from "./operations";
import { LAST_ERROR_LINGER_MS } from "./operationsSlice";
import { clearUndo, pushUndo } from "./undo";

// The top bar shows the newest transition. A refused edit (「This connection
// would create a circular dependency.」) used to stay there through undo,
// redo, a switch to Settings and back — only a reload cleared it. The caller
// reports a refusal where it happened, so the bar's copy is a passing notice:
// it goes once the board has moved on, or after a while. A conflict needs a
// decision and stays.

vi.mock("../api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api")>();
  return {
    ...actual,
    api: {
      getGraph: vi.fn(),
      getState: vi.fn(),
      getTrash: vi.fn(),
      putGraph: vi.fn(),
    },
  };
});

function baseGraph(): Graph {
  return { version: 1, nodes: [{ id: "a" }], edges: [], ui: {} };
}

const refuse = () =>
  useApp.getState().settleOperation(operationScope.graph(), {
    status: "error",
    operation: "node.addDependency",
    message: "This connection would create a circular dependency.",
  });

beforeEach(() => {
  clearUndo();
  vi.mocked(api.getState).mockResolvedValue({ state: { nodes: {}, history: [] }, statuses: {} });
  vi.mocked(api.getGraph).mockResolvedValue({ graph: baseGraph(), rev: "rev-2", statuses: {}, issues: null });
  vi.mocked(api.getTrash).mockResolvedValue({ items: [] });
  vi.mocked(api.putGraph).mockResolvedValue({ ok: true, rev: "rev-2", issues: null });
  useApp.setState({ graph: baseGraph(), graphRev: "rev-1", operations: {}, lastOperation: null, error: null });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("the top bar's error", () => {
  it("goes after a while; the scope keeps its own record", () => {
    vi.useFakeTimers();
    refuse();
    expect(useApp.getState().lastOperation).toMatchObject({ status: "error" });
    vi.advanceTimersByTime(LAST_ERROR_LINGER_MS - 1);
    expect(useApp.getState().lastOperation).toMatchObject({ status: "error" });
    vi.advanceTimersByTime(1);
    expect(useApp.getState().lastOperation).toBeNull();
    expect(useApp.getState().operations.graph).toMatchObject({ status: "error" });
  });

  it("never takes a later transition with it", () => {
    vi.useFakeTimers();
    refuse();
    vi.advanceTimersByTime(LAST_ERROR_LINGER_MS / 2);
    useApp.getState().settleOperation(operationScope.node("a"), { status: "saved", operation: "node.updateMetadata" });
    vi.advanceTimersByTime(LAST_ERROR_LINGER_MS);
    expect(useApp.getState().lastOperation).toMatchObject({ scope: "node:a", status: "saved" });
  });

  it("leaves a conflict up: it needs a decision", () => {
    vi.useFakeTimers();
    useApp.getState().settleOperation(operationScope.graph(), {
      status: "conflict",
      operation: "graph.save",
      message: "changed elsewhere",
    });
    vi.advanceTimersByTime(LAST_ERROR_LINGER_MS * 2);
    expect(useApp.getState().lastOperation).toMatchObject({ status: "conflict" });
  });

  it("goes when an undo lands", async () => {
    pushUndo({ kind: "graph", graph: { version: 1, nodes: [], edges: [], ui: {} } });
    refuse();
    await expect(useApp.getState().undoLast()).resolves.toBe(true);
    expect(useApp.getState().lastOperation).toBeNull();
  });

  it("goes when a redo lands", async () => {
    pushUndo({ kind: "graph", graph: { version: 1, nodes: [], edges: [], ui: {} } });
    await useApp.getState().undoLast();
    refuse();
    await expect(useApp.getState().redoLast()).resolves.toBe(true);
    expect(useApp.getState().lastOperation).toBeNull();
  });
});
