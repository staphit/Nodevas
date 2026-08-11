/**
 * The rubber-band line drawn while a connection is being dragged: where it
 * ends, and why it cannot land there. Refusing a drop is decided here so the
 * preview and the drop itself never disagree.
 */

import {
  dependencyConnectionError,
  logicGateOutputs,
  logicGateRelation,
} from "../../domain";
import type { GraphEdge, LogicGate } from "../../types";
import { LOGIC_GATE_H, LOGIC_GATE_W } from "../canvas/LogicGate";
import type { ConnectionDragState } from "../canvas/useConnectionDrag";
import {
  cardBoundary,
  rectBoundary,
  type Col,
  type Point,
} from "../canvas/geometry";

export function connectionPreview({
  connectionDrag,
  logicGates,
  edges,
  colOf,
  centerX,
  rowTop,
  graphOffsetX,
  graphOffsetY,
}: {
  connectionDrag: ConnectionDragState | null;
  logicGates: LogicGate[];
  edges: GraphEdge[];
  colOf: Map<string, Col>;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  graphOffsetX: number;
  graphOffsetY: number;
}): { previewEnd: Point | null | undefined; previewError: string | null } {
  const connectionTargetGate = connectionDrag?.targetGateId
    ? logicGates.find((gate) => gate.id === connectionDrag.targetGateId)
    : undefined;
  const connectionSourceGate = connectionDrag?.sourceGateId
    ? logicGates.find((gate) => gate.id === connectionDrag.sourceGateId)
    : undefined;
  let connectionPreviewError: string | null = null;
  if (connectionDrag?.sourceId && connectionDrag.targetId) {
    connectionPreviewError = dependencyConnectionError(
      edges,
      connectionDrag.sourceId,
      connectionDrag.targetId,
    );
  } else if (connectionDrag?.sourceId && connectionTargetGate) {
    if (connectionTargetGate.inputs.includes(connectionDrag.sourceId)) {
      connectionPreviewError = "此節點已接到邏輯閘。";
    } else if (
      connectionTargetGate.operator === "must" &&
      connectionTargetGate.inputs.length >= 1
    ) {
      connectionPreviewError = "MUST 僅接受一個輸入節點。";
    }
  } else if (connectionSourceGate && connectionDrag?.targetId) {
    const outputs = logicGateOutputs(connectionSourceGate);
    // A relation gate is many-to-many, so only the same node twice is refused.
    if (outputs.includes(connectionDrag.targetId)) {
      connectionPreviewError = "此邏輯閘已連接該輸出節點。";
    } else if (
      outputs.length > 0 &&
      !logicGateRelation(connectionSourceGate.operator)
    ) {
      connectionPreviewError = "此邏輯閘已有輸出節點。";
    } else if (connectionSourceGate.inputs.includes(connectionDrag.targetId)) {
      connectionPreviewError = "輸出節點不可同時作為此閘門的輸入。";
    }
  }
  const connectionTargetColumn = connectionDrag?.targetId
    ? colOf.get(connectionDrag.targetId)
    : undefined;
  const connectionPreviewEnd =
    connectionDrag && connectionTargetColumn && !connectionPreviewError
      ? cardBoundary(connectionTargetColumn, connectionDrag.start, centerX, rowTop)
      : connectionDrag && connectionTargetGate && !connectionPreviewError
        ? rectBoundary(
            { x: graphOffsetX + connectionTargetGate.x, y: graphOffsetY + connectionTargetGate.y },
            LOGIC_GATE_W / 2,
            LOGIC_GATE_H / 2,
            connectionDrag.start,
          )
        : connectionDrag?.pointer;

  return { previewEnd: connectionPreviewEnd, previewError: connectionPreviewError };
}
