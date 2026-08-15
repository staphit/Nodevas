/**
 * Typed graph commands [A-04].
 *
 * One serializable description per user-visible edit. The store clones the
 * graph, calls {@link applyGraphCommand}, PUTs the result, and rolls back on
 * failure — so a component never needs a whole-graph mutation callback, and a
 * command can be logged, replayed in a test, or labelled in the undo stack.
 *
 * Everything here is pure. Validation throws `CommandError`.
 */

import type {
  CanvasAnnotation,
  CanvasGroup,
  EdgeLine,
  EdgeRelation,
  Graph,
  LogicGateOperator,
  NodeLinkRef,
  NodeStyle,
  SavedView,
  StatusDefinition,
} from "../types";
import { CommandError } from "./errors";
import type { CustomLifecycleStatus, MilestoneType } from "./glossary";
import * as canvas from "./graph/canvas";
import * as gates from "./graph/logicGate";
import { applyImport, type ImportPayload } from "./graph/importer";
import * as node from "./graph/node";
import * as plan from "./graph/plan";
import * as workflow from "./graph/workflow";
import type { DomainId } from "./registry";

export type NodeCommand =
  | { type: "node.updateMetadata"; nodeId: string; patch: node.NodeMetadataPatch }
  | { type: "node.setStyle"; nodeId: string; patch: Partial<NodeStyle> }
  | {
      type: "node.setEntryOverride";
      nodeId: string;
      /** `undefined` restores the automatic rule. */
      value: boolean | undefined;
    }
  | { type: "node.assignByName"; nodeId: string; name: string }
  | { type: "node.assignMany"; nodeIds: string[]; userId?: string }
  | {
      type: "node.addDependency";
      sourceId: string;
      targetId: string;
      /** Defaults to the target's existing top-level operator, else `and`. */
      operator?: string;
      /** Relation of the new edge; "" (required) when omitted. */
      relation?: EdgeRelation;
    }
  | {
      type: "node.setRequires";
      nodeId: string;
      requires: string;
      /** Parsed node references, or `null` when the expression did not parse. */
      refs: string[] | null;
    }
  | { type: "node.setLinks"; nodeId: string; links: NodeLinkRef[] }
  | { type: "node.setUserEmail"; userId: string; email?: string };

export type PlanCommand =
  | { type: "plan.upsert"; nodeId: string; milestone: plan.PlanMilestoneInput }
  | {
      type: "plan.move";
      nodeId: string;
      milestoneType: MilestoneType;
      date: string;
      time?: string;
    }
  | { type: "plan.remove"; nodeId: string; milestoneType: MilestoneType }
  | { type: "plan.clearNode"; nodeId: string };

export type WorkflowCommand =
  | {
      type: "workflow.addLifecycleStatus";
      definition: Omit<StatusDefinition, "id"> & { id?: CustomLifecycleStatus };
    }
  | {
      type: "workflow.updateLifecycleStatus";
      id: CustomLifecycleStatus;
      patch: Partial<Omit<StatusDefinition, "id">>;
    }
  | { type: "workflow.removeLifecycleStatus"; id: CustomLifecycleStatus }
  | { type: "workflow.addMilestoneType"; label: string; id?: `custom-${string}` }
  | { type: "workflow.updateMilestoneType"; id: `custom-${string}`; label: string }
  | {
      type: "workflow.removeMilestoneType";
      id: MilestoneType;
      /** Also drop milestones already scheduled with this type. */
      removeScheduled?: boolean;
    };

export type CanvasCommand =
  | {
      type: "canvas.moveNodes";
      positions: Record<string, canvas.CanvasPoint>;
      /**
       * Slides the rest of the board first, so a drag past the top-left
       * corner keeps everything in the same relative place. Cells for node
       * positions, pixels for the free-floating decorations.
       */
      shift?: { columns: number; rows: number; x: number; y: number };
    }
  | { type: "canvas.setTimelineOrder"; order: string[] }
  | { type: "canvas.setGatePlacement"; targetId: string; point: canvas.CanvasPoint }
  | { type: "canvas.setWireVertices"; wireKey: string; vertices: canvas.CanvasPoint[] }
  | { type: "canvas.removeWireVertex"; wireKey: string; index: number }
  | {
      type: "canvas.setEdgeStyle";
      edges: { from: string; to: string }[];
      /** Left out to keep the current meaning. */
      relation?: EdgeRelation;
      /** Left out to keep the current line. */
      line?: EdgeLine;
    }
  | { type: "canvas.removeEdge"; from: string; to: string }
  | { type: "canvas.upsertGroup"; group: CanvasGroup }
  | { type: "canvas.removeGroup"; id: string }
  | { type: "canvas.upsertAnnotation"; annotation: CanvasAnnotation }
  | { type: "canvas.removeAnnotation"; id: string }
  | { type: "canvas.saveView"; view: SavedView }
  | { type: "canvas.removeView"; id: string }
  | {
      type: "canvas.createLogicGate";
      operator: LogicGateOperator;
      x: number;
      y: number;
    }
  | {
      type: "canvas.convertEdgesToLogicGate";
      edges: { from: string; to: string }[];
      x: number;
      y: number;
    }
  | { type: "canvas.moveLogicGate"; gateId: string; point: canvas.CanvasPoint }
  | { type: "canvas.connectLogicGateInput"; gateId: string; sourceId: string }
  | { type: "canvas.disconnectLogicGateInput"; gateId: string; sourceId: string }
  | { type: "canvas.setLogicGateOutput"; gateId: string; targetId?: string }
  | {
      type: "canvas.toggleLogicGateOutput";
      gateId: string;
      targetId: string;
      enabled: boolean;
    }
  | { type: "canvas.setLogicGateOperator"; gateId: string; operator: LogicGateOperator }
  | { type: "canvas.removeLogicGate"; gateId: string };

/** Whole-graph operations that import or replace graph data. */
export type ImportCommand = { type: "graph.applyImport"; payload: ImportPayload };

export type GraphCommand =
  | ImportCommand
  | NodeCommand
  | PlanCommand
  | WorkflowCommand
  | CanvasCommand;

export type GraphCommandType = GraphCommand["type"];

/** Applies a command to a graph draft in place. Throws on invalid input. */
export function applyGraphCommand(graph: Graph, command: GraphCommand): void {
  applyGraphCommandStep(graph, command);
  // Any command may have rewritten the edge list; nothing may point at a wire
  // that is gone.
  canvas.pruneEdgeDecorations(graph);
}

function applyGraphCommandStep(graph: Graph, command: GraphCommand): void {
  switch (command.type) {
    case "graph.applyImport":
      return applyImport(graph, command.payload);

    case "node.updateMetadata":
      return node.updateNodeMetadata(graph, command.nodeId, command.patch);
    case "node.setStyle":
      return node.setNodeStyle(graph, command.nodeId, command.patch);
    case "node.setEntryOverride":
      return node.setNodeEntryOverride(graph, command.nodeId, command.value);
    case "node.assignByName":
      void node.assignNodeByName(graph, command.nodeId, command.name);
      return;
    case "node.assignMany":
      return node.assignNodes(graph, command.nodeIds, command.userId);
    case "node.addDependency":
      return node.addDependency(graph, command.sourceId, command.targetId, {
        operator: command.operator,
        relation: command.relation,
      });
    case "node.setRequires":
      return node.setNodeRequires(
        graph,
        command.nodeId,
        command.requires,
        command.refs,
      );
    case "node.setLinks":
      return node.setNodeLinks(graph, command.nodeId, command.links);
    case "node.setUserEmail":
      return node.setUserEmail(graph, command.userId, command.email);

    case "plan.upsert":
      return plan.upsertPlanMilestone(graph, command.nodeId, command.milestone);
    case "plan.move":
      return plan.movePlanMilestone(
        graph,
        command.nodeId,
        command.milestoneType,
        command.date,
        command.time,
      );
    case "plan.remove":
      return plan.removePlanMilestone(graph, command.nodeId, command.milestoneType);
    case "plan.clearNode":
      return plan.clearNodePlans(graph, command.nodeId);

    case "workflow.addLifecycleStatus":
      void workflow.addLifecycleStatus(graph, command.definition);
      return;
    case "workflow.updateLifecycleStatus":
      return workflow.updateLifecycleStatus(graph, command.id, command.patch);
    case "workflow.removeLifecycleStatus":
      return workflow.removeLifecycleStatus(graph, command.id);
    case "workflow.addMilestoneType":
      void workflow.addMilestoneType(graph, {
        label: command.label,
        id: command.id,
      });
      return;
    case "workflow.updateMilestoneType":
      return workflow.updateMilestoneType(graph, command.id, {
        label: command.label,
      });
    case "workflow.removeMilestoneType":
      return workflow.removeMilestoneType(graph, command.id, {
        removeScheduled: command.removeScheduled,
      });

    case "canvas.moveNodes":
      if (command.shift) canvas.shiftBoard(graph, command.shift);
      return canvas.moveNodes(graph, command.positions);
    case "canvas.setTimelineOrder":
      return canvas.setTimelineOrder(graph, command.order);
    case "canvas.setGatePlacement":
      return canvas.setGatePlacement(graph, command.targetId, command.point);
    case "canvas.setWireVertices":
      return canvas.setWireVertices(graph, command.wireKey, command.vertices);
    case "canvas.removeWireVertex":
      return canvas.removeWireVertex(graph, command.wireKey, command.index);
    case "canvas.setEdgeStyle":
      return canvas.setEdgeStyle(graph, command.edges, {
        relation: command.relation,
        line: command.line,
      });
    case "canvas.removeEdge":
      return canvas.removeEdge(graph, command.from, command.to);
    case "canvas.upsertGroup":
      return canvas.upsertGroup(graph, command.group);
    case "canvas.removeGroup":
      return canvas.removeGroup(graph, command.id);
    case "canvas.upsertAnnotation":
      return canvas.upsertAnnotation(graph, command.annotation);
    case "canvas.removeAnnotation":
      return canvas.removeAnnotation(graph, command.id);
    case "canvas.saveView":
      return canvas.saveView(graph, command.view);
    case "canvas.removeView":
      return canvas.removeView(graph, command.id);
    case "canvas.createLogicGate":
      void gates.createLogicGate(graph, {
        operator: command.operator,
        x: command.x,
        y: command.y,
      });
      return;
    case "canvas.convertEdgesToLogicGate":
      void gates.convertEdgesToLogicGate(graph, command.edges, {
        x: command.x,
        y: command.y,
      });
      return;
    case "canvas.moveLogicGate":
      return gates.moveLogicGate(graph, command.gateId, command.point);
    case "canvas.connectLogicGateInput":
      return gates.connectLogicGateInput(graph, command.gateId, command.sourceId);
    case "canvas.disconnectLogicGateInput":
      return gates.disconnectLogicGateInput(graph, command.gateId, command.sourceId);
    case "canvas.setLogicGateOutput":
      return gates.setLogicGateOutput(graph, command.gateId, command.targetId);
    case "canvas.toggleLogicGateOutput":
      return gates.toggleLogicGateOutput(
        graph,
        command.gateId,
        command.targetId,
        command.enabled,
      );
    case "canvas.setLogicGateOperator":
      return gates.setLogicGateOperator(graph, command.gateId, command.operator);
    case "canvas.removeLogicGate":
      return gates.removeLogicGate(graph, command.gateId);
    default: {
      const exhaustive: never = command;
      throw new CommandError(
        "unsupported",
        `未知的指令：${JSON.stringify(exhaustive)}`,
      );
    }
  }
}

const COMMAND_LABELS: Record<GraphCommandType, string> = {
  "graph.applyImport": "匯入節點",
  "node.updateMetadata": "修改節點資料",
  "node.setStyle": "調整節點外觀",
  "node.setEntryOverride": "設定起點標記",
  "node.assignByName": "指派負責人",
  "node.assignMany": "批次指派負責人",
  "node.addDependency": "建立前置關係",
  "node.setRequires": "修改前置條件",
  "node.setLinks": "編輯節點連結",
  "node.setUserEmail": "修改成員信箱",
  "plan.upsert": "設定預期里程碑",
  "plan.move": "調整預期里程碑日期",
  "plan.remove": "刪除預期里程碑",
  "plan.clearNode": "清除節點預期計畫",
  "workflow.addLifecycleStatus": "新增實際狀態定義",
  "workflow.updateLifecycleStatus": "修改實際狀態定義",
  "workflow.removeLifecycleStatus": "刪除實際狀態定義",
  "workflow.addMilestoneType": "新增里程碑類型",
  "workflow.updateMilestoneType": "修改里程碑類型",
  "workflow.removeMilestoneType": "刪除里程碑類型",
  "canvas.moveNodes": "移動節點",
  "canvas.setTimelineOrder": "調整時間軸順序",
  "canvas.setGatePlacement": "移動條件閘",
  "canvas.setWireVertices": "調整連線轉折點",
  "canvas.removeWireVertex": "刪除連線轉折點",
  "canvas.setEdgeStyle": "調整關係樣式",
  "canvas.removeEdge": "刪除關係",
  "canvas.upsertGroup": "編輯群組底圖",
  "canvas.removeGroup": "刪除群組底圖",
  "canvas.upsertAnnotation": "編輯註解",
  "canvas.removeAnnotation": "刪除註解",
  "canvas.saveView": "儲存檢視",
  "canvas.removeView": "刪除檢視",
  "canvas.createLogicGate": "新增邏輯閘",
  "canvas.convertEdgesToLogicGate": "關係線轉為邏輯閘",
  "canvas.moveLogicGate": "移動邏輯閘",
  "canvas.connectLogicGateInput": "連接邏輯閘輸入",
  "canvas.disconnectLogicGateInput": "移除邏輯閘輸入",
  "canvas.setLogicGateOutput": "設定邏輯閘輸出",
  "canvas.toggleLogicGateOutput": "調整邏輯閘輸出",
  "canvas.setLogicGateOperator": "變更邏輯閘運算子",
  "canvas.removeLogicGate": "刪除邏輯閘",
};

/** Undo label / operation description, in UI wording. */
export function describeGraphCommand(command: GraphCommand): string {
  return COMMAND_LABELS[command.type] ?? command.type;
}

export interface CommandTarget {
  domain: DomainId;
  /** Present when the edit belongs to one node, so the UI can scope its badge. */
  nodeId?: string;
}

/** Which registry domain a command writes, for operation scoping. */
export function commandTarget(command: GraphCommand): CommandTarget {
  if (command.type.startsWith("graph.")) return { domain: "project-layout" };
  if (command.type.startsWith("plan.")) {
    return { domain: "plan-milestone", nodeId: (command as PlanCommand).nodeId };
  }
  if (command.type.startsWith("workflow.")) return { domain: "workflow" };
  if (command.type.startsWith("canvas.")) return { domain: "project-layout" };
  const nodeId = "nodeId" in command ? command.nodeId : undefined;
  return { domain: "node-metadata", nodeId };
}
