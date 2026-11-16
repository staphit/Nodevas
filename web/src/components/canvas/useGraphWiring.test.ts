import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { commandFailed, commandSucceeded, type CommandResult } from "../../state/operations";
import { useApp } from "../../store";
import type { Graph } from "../../types";
import { useGraphWiring } from "./useGraphWiring";

const graph: Graph = {
  version: 1,
  nodes: [
    { id: "a", title: "Alpha" },
    { id: "b", title: "Bravo" },
  ],
  edges: [],
  ui: { logicGates: [{ id: "gate-1", operator: "and", x: 0, y: 0, inputs: [] }] },
};

function wiring(result: CommandResult) {
  const updateCanvasLayout = vi.fn(async () => result);
  const { result: hook } = renderHook(() =>
    useGraphWiring({
      graph,
      nodeTitle: (id) => id ?? "",
      selectNode: vi.fn(),
      setGraphNotice: vi.fn(),
      setGraphSelection: vi.fn(),
      setContextMenu: vi.fn(),
      updateNode: vi.fn(async () => commandSucceeded("node", "graph", undefined)),
      updateCanvasLayout,
      runCanvasCommand: vi.fn(),
    }),
  );
  return { hook: hook.current, updateCanvasLayout };
}

const dismissed = () => useApp.getState().preferences.graphConnectTipDismissed;

beforeEach(() => {
  useApp.getState().updateUIPreference("language", "en");
  useApp.getState().updateUIPreference("graphConnectTipDismissed", false);
});

// A first dependency made through a logic gate left the connect tip up, to
// come back on every graph without one: only card-to-card wires counted.
describe("wiring through a logic gate", () => {
  it("dismisses the connect tip once a card feeds the gate", async () => {
    const { hook, updateCanvasLayout } = wiring(commandSucceeded("canvas", "graph", undefined));
    hook.connectNodeToLogicGate("a", "gate-1");
    await vi.waitFor(() => expect(dismissed()).toBe(true));
    expect(updateCanvasLayout).toHaveBeenCalledWith(
      expect.objectContaining({ type: "canvas.connectLogicGateInput", gateId: "gate-1", sourceId: "a" }),
    );
  });

  it("dismisses the connect tip once the gate drives a card", async () => {
    const { hook, updateCanvasLayout } = wiring(commandSucceeded("canvas", "graph", undefined));
    hook.connectLogicGateToNode("gate-1", "b");
    await vi.waitFor(() => expect(dismissed()).toBe(true));
    expect(updateCanvasLayout).toHaveBeenCalledWith(
      expect.objectContaining({ type: "canvas.toggleLogicGateOutput", gateId: "gate-1", targetId: "b" }),
    );
  });

  it("keeps the tip when the hub refuses the wire", async () => {
    const { hook, updateCanvasLayout } = wiring(commandFailed("canvas", "graph", "refused"));
    hook.connectNodeToLogicGate("a", "gate-1");
    hook.connectLogicGateToNode("gate-1", "b");
    expect(updateCanvasLayout).toHaveBeenCalledTimes(2);
    // The hook's own handlers on these results run before this await ends.
    await Promise.all(updateCanvasLayout.mock.results.map((call) => call.value));
    expect(dismissed()).toBe(false);
  });
});
