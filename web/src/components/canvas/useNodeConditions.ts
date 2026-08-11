/**
 * The condition menu on a node: what its "requires" expression is made of.
 *
 * A node's prerequisites can be plain edges, a gate expression over several
 * sources, or a standalone logic gate wired to it. This gathers what the menu
 * offers — which sources are still connectable, which gate controls the node,
 * what operator is editable — and the edits that follow from picking one.
 */

import { useState } from "react";
import { topLevelOp } from "../../store";
import {
  editableGateOperator,
  logicGateOutputs,
  nextLogicGateID,
} from "../../domain";
import type { CanvasCommand, NodeCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import type {
  EdgeRelation,
  Graph,
  LogicGate,
  LogicGateOperator,
} from "../../types";
import type { GraphSelection, LaneContextMenu } from "../LaneView";
import { COL_W, ROW_H } from "./geometry";

const EDITABLE_GATE_OPERATORS = new Set(["and", "or", "xor", "nand", "nor"]);

export function useNodeConditions({
  graph,
  logicGates,
  contextMenu,
  setContextMenu,
  setGraphSelection,
  setGraphNotice,
  updateNode,
  updateCanvasLayout,
  runCanvasCommand,
}: {
  graph: Graph | null;
  logicGates: LogicGate[];
  contextMenu: LaneContextMenu | null;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  setGraphSelection: (selection: GraphSelection) => void;
  setGraphNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
  updateNode: (command: NodeCommand) => Promise<CommandResult>;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;
  runCanvasCommand: (command: CanvasCommand) => void;
}) {
  const [conditionSource, setConditionSource] = useState("");
  const [conditionOperator, setConditionOperator] = useState("and");
  const [conditionRelation, setConditionRelation] = useState<EdgeRelation>("");

  const openConditionMenu = (nodeId: string, x: number, y: number) => {
    const incoming = new Set(
      (graph?.edges ?? []).filter((edge) => edge.to === nodeId).map((edge) => edge.from),
    );
    const firstSource = (graph?.nodes ?? []).find(
      (node) => node.id !== nodeId && !incoming.has(node.id),
    );
    const target = graph?.nodes?.find((node) => node.id === nodeId);
    setConditionSource(firstSource?.id ?? "");
    setConditionOperator(editableGateOperator(topLevelOp(target?.requires)));
    setConditionRelation("");
    setContextMenu({ kind: "node", nodeId, x, y });
  };

  /**
   * Replaces the inline badge on a set of relation edges with the gate
   * that owns them. Placed at the middle of the nodes it connects, so the new
   * glyph lands where the badge used to be.
   */
  const convertEdgesToLogicGate = (
    edges: { from: string; to: string }[],
    menu: { x: number; y: number },
  ) => {
    const stored = graph?.ui?.positions ?? {};
    const involved = [...new Set(edges.flatMap((edge) => [edge.from, edge.to]))];
    const points = involved.flatMap((id) => (stored[id] ? [stored[id]] : []));
    const average = (values: number[]) =>
      values.length > 0 ? values.reduce((sum, value) => sum + value, 0) / values.length : 0;
    const createdID = nextLogicGateID(graph?.ui?.logicGates ?? []);
    void updateCanvasLayout({
      type: "canvas.convertEdgesToLogicGate",
      edges,
      x: average(points.map((point) => point.x)) * COL_W + COL_W / 2,
      y: average(points.map((point) => point.y)) * ROW_H + ROW_H / 2,
    }).then((result) => {
      if (!result.ok) {
        setGraphNotice({ text: result.message, kind: "error" });
        return;
      }
      setGraphSelection({ kind: "logic-gate", id: createdID });
      setGraphNotice({ text: "已將關係線收進邏輯閘。", kind: "ok" });
      setContextMenu({ kind: "logic-gate", gateId: createdID, x: menu.x, y: menu.y });
    });
  };

  const createStandaloneLogicGate = (
    menu: Extract<LaneContextMenu, { kind: "graph" }>,
    operator: LogicGateOperator,
  ) => {
    // The id allocator is deterministic, so the new gate's id is known before
    // the command runs and the selection can follow it.
    const createdID = nextLogicGateID(graph?.ui?.logicGates ?? []);
    void updateCanvasLayout({
      type: "canvas.createLogicGate",
      operator,
      x: menu.col * COL_W + COL_W / 2,
      y: menu.row * ROW_H + ROW_H / 2,
    }).then((result) => {
      if (!result.ok) {
        setGraphNotice({ text: result.message, kind: "error" });
        return;
      }
      setGraphSelection({ kind: "logic-gate", id: createdID });
      setGraphNotice({
        text: `${operator.toUpperCase()} 已建立；請選擇輸入節點與輸出節點。`,
        kind: "ok",
      });
      setContextMenu({
        kind: "logic-gate",
        gateId: createdID,
        x: menu.x,
        y: menu.y,
      });
    });
  };

  const addDependency = (targetId: string) => {
    if (!conditionSource || conditionSource === targetId) return;
    void updateNode({
      type: "node.addDependency",
      sourceId: conditionSource,
      targetId,
      operator: conditionOperator,
      relation: conditionRelation,
    }).then((result) => {
      if (!result.ok) setGraphNotice({ text: result.message, kind: "error" });
    });
    setContextMenu(null);
  };

  const changeGateOperator = (targetId: string, nextOperator: string) => {
    const target = graph?.nodes?.find((node) => node.id === targetId);
    if (target?.requires) {
      // Swap only the top-level operators; nested groups keep their own.
      let depth = 0;
      const requires = target.requires.replace(
        /\b(and|or|xor|nand|nor)\b|[()]/gi,
        (token) => {
          if (token === "(") {
            depth++;
            return token;
          }
          if (token === ")") {
            depth--;
            return token;
          }
          return depth === 0 ? nextOperator : token;
        },
      );
      void updateNode({
        type: "node.setRequires",
        nodeId: targetId,
        requires,
        refs: null,
      }).then((result) => {
        if (!result.ok) reportError(new Error(result.message));
      });
    }
    setConditionOperator(nextOperator);
  };

  const setDependencyRelation = (
    targetId: string,
    sourceId: string,
    relation: EdgeRelation,
  ) => {
    runCanvasCommand({
      type: "canvas.setEdgeStyle",
      edges: [{ from: sourceId, to: targetId }],
      relation,
    });
  };

  const conditionTarget =
    contextMenu?.kind === "node"
      ? graph?.nodes?.find((node) => node.id === contextMenu.nodeId)
      : undefined;
  const editedLogicGate =
    contextMenu?.kind === "logic-gate"
      ? logicGates.find((gate) => gate.id === contextMenu.gateId)
      : undefined;
  const controllingLogicGate =
    contextMenu?.kind === "node"
      ? logicGates.find((gate) =>
          logicGateOutputs(gate).includes(contextMenu.nodeId),
        )
      : undefined;
  const assignableLogicGates =
    contextMenu?.kind === "node"
      ? logicGates.filter((gate) => {
          const outputs = logicGateOutputs(gate);
          return outputs.length === 0 || outputs.includes(contextMenu.nodeId);
        })
      : [];
  const incomingConditions =
    contextMenu?.kind === "node"
      ? (graph?.edges ?? []).filter((edge) => edge.to === contextMenu.nodeId)
      : [];
  const incomingSourceIDs = new Set(incomingConditions.map((edge) => edge.from));
  const availableConditionSources =
    contextMenu?.kind === "node"
      ? (graph?.nodes ?? []).filter(
          (node) => node.id !== contextMenu.nodeId && !incomingSourceIDs.has(node.id),
        )
      : [];
  const currentGateOperator = topLevelOp(conditionTarget?.requires);
  const hasEditableGateOperator =
    currentGateOperator !== null && EDITABLE_GATE_OPERATORS.has(currentGateOperator);

  return {
    conditionSource,
    setConditionSource,
    conditionOperator,
    conditionRelation,
    setConditionRelation,
    conditionTarget,
    editedLogicGate,
    controllingLogicGate,
    assignableLogicGates,
    incomingConditions,
    availableConditionSources,
    currentGateOperator,
    hasEditableGateOperator,
    openConditionMenu,
    createStandaloneLogicGate,
    convertEdgesToLogicGate,
    addDependency,
    changeGateOperator,
    setDependencyRelation,
  };
}
