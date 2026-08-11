import { beforeEach, describe, expect, it } from "vitest";
import { useApp } from "../store";
import { operationScope, setOperationClock } from "./operations";

// This slice has no async calls of its own — it is the pure `set`
// transitions other slices call into — so no api mocking is needed, just a
// deterministic clock so `at` is assertable.
let now = 1000;
beforeEach(() => {
  now = 1000;
  setOperationClock(() => now);
  useApp.setState({ operations: {}, lastOperation: null });
});

describe("beginOperation", () => {
  it("marks the scope pending and becomes the newest transition", () => {
    useApp.getState().beginOperation(operationScope.node("a"), "node.updateMetadata");

    expect(useApp.getState().operations["node:a"]).toEqual({
      status: "pending",
      operation: "node.updateMetadata",
      at: 1000,
    });
    expect(useApp.getState().lastOperation).toEqual({
      scope: "node:a",
      status: "pending",
      operation: "node.updateMetadata",
      at: 1000,
    });
  });
});

describe("settleOperation", () => {
  it("moves a pending scope to saved and stamps the transition time", () => {
    useApp.getState().beginOperation(operationScope.graph(), "graph.save");
    now = 1200;

    useApp.getState().settleOperation(operationScope.graph(), { status: "saved", operation: "graph.save" });

    expect(useApp.getState().operations.graph).toEqual({
      status: "saved",
      operation: "graph.save",
      at: 1200,
    });
    expect(useApp.getState().lastOperation).toMatchObject({ scope: "graph", status: "saved" });
  });

  it("keeps two scopes independent so one panel's error does not bleed into another's badge", () => {
    useApp.getState().beginOperation(operationScope.node("a"), "node.updateMetadata");
    useApp.getState().beginOperation(operationScope.node("b"), "node.updateMetadata");

    useApp
      .getState()
      .settleOperation(operationScope.node("a"), { status: "error", operation: "node.updateMetadata", message: "失敗" });

    expect(useApp.getState().operations["node:a"]).toMatchObject({ status: "error" });
    expect(useApp.getState().operations["node:b"]).toMatchObject({ status: "pending" });
  });
});

describe("clearOperation", () => {
  it("drops the scope back to idle, which the map represents as absent", () => {
    useApp.getState().beginOperation(operationScope.node("a"), "node.updateMetadata");

    useApp.getState().clearOperation(operationScope.node("a"));

    expect(useApp.getState().operations["node:a"]).toBeUndefined();
  });

  it("only clears lastOperation when it belonged to the cleared scope", () => {
    useApp.getState().beginOperation(operationScope.node("a"), "node.updateMetadata");
    useApp.getState().beginOperation(operationScope.node("b"), "node.updateMetadata");
    // lastOperation now points at "b".

    useApp.getState().clearOperation(operationScope.node("a"));
    expect(useApp.getState().lastOperation).toMatchObject({ scope: "node:b" });

    useApp.getState().clearOperation(operationScope.node("b"));
    expect(useApp.getState().lastOperation).toBeNull();
  });
});
