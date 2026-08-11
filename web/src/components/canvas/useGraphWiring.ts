/**
 * Writing dependency wiring back to the graph.
 *
 * Connecting two nodes, feeding a logic gate, giving it an output and
 * removing it are all edits to the same relationships, and each has its own
 * rules about what is allowed. The gesture that triggers them lives in
 * useConnectionDrag; what they mean lives here.
 */

import {
  dependencyConnectionError,
  logicGateInputRequirement,
  logicGateIsComplete,
  logicGateOutputs,
  logicGateRelation,
} from "../../domain";
import type { CanvasCommand, NodeCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import type { Graph, LogicGateOperator } from "../../types";

export function useGraphWiring({
  graph,
  nodeTitle,
  selectNode,
  setGraphNotice,
  setGraphSelection,
  setContextMenu,
  updateNode,
  updateCanvasLayout,
  runCanvasCommand,
}: {
  graph: Graph | null;
  nodeTitle: (id: string | undefined) => string;
  selectNode: (nodeId: string) => void;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
  setGraphSelection: (selection: null) => void;
  setContextMenu: (menu: null) => void;
  updateNode: (command: NodeCommand) => Promise<CommandResult>;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;
  /** Fire-and-forget layout write: failures surface through reportError. */
  runCanvasCommand: (command: CanvasCommand) => void;
}) {
  const connectNodes = (sourceId: string, targetId: string) => {
    const error = dependencyConnectionError(graph?.edges ?? [], sourceId, targetId);
    if (error) {
      setGraphNotice({ text: error, kind: "error" });
      return;
    }
    void updateNode({ type: "node.addDependency", sourceId, targetId }).then(
      (result) => {
        if (!result.ok) {
          setGraphNotice({ text: result.message, kind: "error" });
          return;
        }
        setGraphNotice({
          text: `已建立：${nodeTitle(sourceId)} → ${nodeTitle(targetId)}`,
          kind: "ok",
        });
        selectNode(targetId);
      },
    );
  };

  const connectNodeToLogicGate = (sourceId: string, gateId: string) => {
    const gate = graph?.ui?.logicGates?.find((candidate) => candidate.id === gateId);
    if (!gate) return;
    if (gate.inputs.includes(sourceId)) {
      setGraphNotice({ text: "此節點已接到邏輯閘。", kind: "error" });
      return;
    }
    if (gate.operator === "must" && gate.inputs.length >= 1) {
      setGraphNotice({ text: "MUST 僅接受一個輸入節點。", kind: "error" });
      return;
    }
    // A relation gate writes edges that nothing waits on, so a loop through it
    // cannot deadlock anything and is left to the user.
    if (!logicGateRelation(gate.operator)) {
      for (const output of logicGateOutputs(gate)) {
        if ((graph?.edges ?? []).some((e) => e.from === sourceId && e.to === output)) {
          continue;
        }
        const error = dependencyConnectionError(graph?.edges ?? [], sourceId, output);
        if (error) {
          setGraphNotice({ text: error, kind: "error" });
          return;
        }
      }
    }
    void updateCanvasLayout({
      type: "canvas.connectLogicGateInput",
      gateId,
      sourceId,
    }).then((result) => {
      if (!result.ok) {
        setGraphNotice({ text: result.message, kind: "error" });
        return;
      }
      const required = logicGateInputRequirement(gate.operator);
      const nextCount = gate.inputs.length + 1;
      setGraphNotice({
        text:
          gate.output && nextCount >= required
            ? "邏輯閘接線完成，條件已生效。"
            : `已接上輸入；${gate.output ? `至少需要 ${required} 個輸入` : "仍需連接輸出節點"}。`,
        kind: "ok",
      });
    });
  };

  const connectLogicGateToNode = (gateId: string, targetId: string) => {
    const gate = graph?.ui?.logicGates?.find((candidate) => candidate.id === gateId);
    if (!gate) return;
    const outputs = logicGateOutputs(gate);
    if (outputs.includes(targetId)) {
      setGraphNotice({ text: "此邏輯閘已連接該輸出節點。", kind: "error" });
      return;
    }
    // A relation gate is many-to-many; a boolean gate drives a single node.
    if (outputs.length > 0 && !logicGateRelation(gate.operator)) {
      setGraphNotice({ text: "此邏輯閘已有輸出節點。", kind: "error" });
      return;
    }
    if (gate.inputs.includes(targetId)) {
      setGraphNotice({ text: "輸出節點不可同時作為此閘門的輸入。", kind: "error" });
      return;
    }
    const otherGate = graph?.ui?.logicGates?.find(
      (candidate) =>
        candidate.id !== gateId && logicGateOutputs(candidate).includes(targetId),
    );
    if (otherGate) {
      setGraphNotice({ text: "此節點已由另一個邏輯閘控制。", kind: "error" });
      return;
    }
    if (!logicGateRelation(gate.operator)) {
      for (const input of gate.inputs) {
        if ((graph?.edges ?? []).some((edge) => edge.from === input && edge.to === targetId)) {
          continue;
        }
        const error = dependencyConnectionError(graph?.edges ?? [], input, targetId);
        if (error) {
          setGraphNotice({ text: error, kind: "error" });
          return;
        }
      }
    }
    void updateCanvasLayout({
      type: "canvas.toggleLogicGateOutput",
      gateId,
      targetId,
      enabled: true,
    }).then((result) => {
      if (!result.ok) {
        setGraphNotice({ text: result.message, kind: "error" });
        return;
      }
      setGraphNotice({
        text: logicGateIsComplete({ ...gate, outputs: [...outputs, targetId] })
          ? "邏輯閘接線完成，條件已生效。"
          : `已接上輸出；仍需至少 ${logicGateInputRequirement(gate.operator)} 個輸入。`,
        kind: "ok",
      });
    });
  };

  /** Adds or removes one output of a relation gate without touching the rest. */
  const toggleLogicGateOutput = (
    gateId: string,
    targetId: string,
    enabled: boolean,
  ) => {
    const gate = graph?.ui?.logicGates?.find((candidate) => candidate.id === gateId);
    if (!gate) return;
    if (enabled) {
      connectLogicGateToNode(gateId, targetId);
      return;
    }
    void updateCanvasLayout({
      type: "canvas.toggleLogicGateOutput",
      gateId,
      targetId,
      enabled: false,
    }).then((result) => {
      if (!result.ok) setGraphNotice({ text: result.message, kind: "error" });
    });
  };

  const setLogicGateOutput = (gateId: string, targetId: string) => {
    const gate = graph?.ui?.logicGates?.find((candidate) => candidate.id === gateId);
    if (!gate) return;
    if (targetId && gate.inputs.includes(targetId)) {
      setGraphNotice({ text: "輸出節點不可同時作為此閘門的輸入。", kind: "error" });
      return;
    }
    const otherGate = graph?.ui?.logicGates?.find(
      (candidate) =>
        candidate.id !== gateId && logicGateOutputs(candidate).includes(targetId),
    );
    if (targetId && otherGate) {
      setGraphNotice({ text: "此節點已由另一個邏輯閘控制。", kind: "error" });
      return;
    }
    if (targetId && !logicGateRelation(gate.operator)) {
      for (const input of gate.inputs) {
        const owned = logicGateOutputs(gate);
        const remainingEdges = (graph?.edges ?? []).filter(
          (edge) => !owned.includes(edge.to) || !gate.inputs.includes(edge.from),
        );
        const error = dependencyConnectionError(remainingEdges, input, targetId);
        if (error) {
          setGraphNotice({ text: error, kind: "error" });
          return;
        }
      }
    }
    void updateCanvasLayout({
      type: "canvas.setLogicGateOutput",
      gateId,
      targetId: targetId || undefined,
    }).then((result) => {
      if (!result.ok) {
        setGraphNotice({ text: result.message, kind: "error" });
        return;
      }
      setGraphNotice({
        text: targetId
          ? `已將 ${gate.operator.toUpperCase()} 指派給「${nodeTitle(targetId)}」${
              logicGateIsComplete({ ...gate, output: targetId, outputs: [targetId] })
                ? "，條件已生效。"
                : `；仍需 ${logicGateInputRequirement(gate.operator)} 個輸入。`
            }`
          : "已解除邏輯閘的輸出節點。",
        kind: "ok",
      });
    });
  };

  const assignExistingLogicGateToNode = (targetId: string, gateId: string) => {
    const current = graph?.ui?.logicGates?.find((gate) =>
      logicGateOutputs(gate).includes(targetId),
    );
    if (!gateId) {
      if (current) setLogicGateOutput(current.id, "");
      return;
    }
    if (!current || current.id === gateId) {
      setLogicGateOutput(gateId, targetId);
      return;
    }
    const replacement = graph?.ui?.logicGates?.find((gate) => gate.id === gateId);
    if (!replacement || logicGateOutputs(replacement).length > 0) return;
    // Hand the output over: detach the current gate first so the target is
    // never controlled by two gates at once.
    void updateCanvasLayout({
      type: "canvas.setLogicGateOutput",
      gateId: current.id,
      targetId: undefined,
    })
      .then((detached) => {
        if (!detached.ok) return detached;
        return updateCanvasLayout({
          type: "canvas.setLogicGateOutput",
          gateId,
          targetId,
        });
      })
      .then((result) => {
        if (!result.ok) {
          setGraphNotice({ text: result.message, kind: "error" });
          return;
        }
        setGraphNotice({
          text: `已改由 ${replacement.operator.toUpperCase()} · ${replacement.id} 控制「${nodeTitle(targetId)}」。`,
          kind: "ok",
        });
      });
  };

  const toggleLogicGateInput = (gateId: string, nodeId: string, enabled: boolean) => {
    const gate = graph?.ui?.logicGates?.find((candidate) => candidate.id === gateId);
    if (!gate) return;
    if (enabled) {
      connectNodeToLogicGate(nodeId, gateId);
      return;
    }
    runCanvasCommand({
      type: "canvas.disconnectLogicGateInput",
      gateId,
      sourceId: nodeId,
    });
  };

  const setStandaloneLogicGateOperator = (
    gateId: string,
    operator: LogicGateOperator,
  ) => {
    runCanvasCommand({ type: "canvas.setLogicGateOperator", gateId, operator });
  };

  const deleteStandaloneLogicGate = (gateId: string) => {
    void updateCanvasLayout({ type: "canvas.removeLogicGate", gateId }).then(
      (result) => {
        if (!result.ok) {
          reportError(new Error(result.message));
          return;
        }
        setContextMenu(null);
        setGraphSelection(null);
      },
    );
  };

  return {
    connectNodes,
    connectNodeToLogicGate,
    connectLogicGateToNode,
    setLogicGateOutput,
    toggleLogicGateOutput,
    assignExistingLogicGateToNode,
    toggleLogicGateInput,
    setStandaloneLogicGateOperator,
    deleteStandaloneLogicGate,
  };
}
