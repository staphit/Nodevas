/**
 * Inline dependency gate [B-06].
 *
 * Drawn in front of a node that has a `requires` expression but no first-class
 * logic gate. It renders the ordinary dependency fan-in that remains outside
 * first-class gates.
 */

import { useState, useEffect, useRef } from "react";
import {
  ROW_H,
  WIRE_CLEARANCE,
  averagePoint,
  cardBoundary,
  cardCenter,
  constrainPointOutsideRects,
  edgePlacementKey,
  gateWireKey,
  nearestClearRatio,
  pointOnLine,
  rectBoundary,
  svgPointerPoint,
  type Col,
  type Point,
  type Rect,
} from "./geometry";
import type { EdgeLine, EdgeRelation } from "../../types";
import { DependencyLine } from "./DependencyLine";



// ---------- dependency-gate wiring ----------

export interface Gate {
  to: Col;
  parents: {
    from: Col;
    relation: EdgeRelation;
    line: Exclude<EdgeLine, "">;
    /** Source node is out of play, so the wire is drawn faded. */
    muted: boolean;
  }[];
  op: string | null;
}

export const GATE_W = 76;
export const GATE_H = 24;
export const DEFAULT_GATE_RATIO = 0.72;

export const BINARY_GATE_OPS = new Set(["and", "or", "xor", "nand", "nor"]);

export const OP_PRESENTATION: Record<string, { label: string; description: string }> = {
  optional: { label: "選用", description: "此關係為選用分支，不阻擋進行" },
  deprecated: { label: "棄用", description: "此關係已棄用，不列入前置條件" },
  and: { label: "全部 · AND", description: "全部條件都成立" },
  or: { label: "任一 · OR", description: "至少一個條件成立" },
  xor: { label: "擇一 · XOR", description: "只有一個條件成立" },
  not: { label: "反向 · NOT", description: "條件不成立" },
  nand: { label: "非全部 · NAND", description: "不是全部條件都成立" },
  nor: { label: "皆非 · NOR", description: "所有條件都不成立" },
};

export function GateWiring({
  gate,
  centerX,
  rowTop,
  storedPoint,
  storedRatio,
  onPositionChange,
  selectedEdges,
  onSelectEdge,
  onOpenEdgeMenu,
  getWireVertices,
  onWireVerticesChange,
  selectedVertex,
  onSelectVertex,
  obstacles,
  onEditGate,
  onDeleteGate,
}: {
  gate: Gate;
  centerX: (c: Col) => number;
  rowTop: (row: number) => number;
  storedPoint?: Point;
  storedRatio?: number;
  onPositionChange: (targetId: string, point: Point) => void;
  /** Wire keys drawn as selected: the picked edge, plus any wire whose two
      endpoints are both inside a marquee selection. */
  selectedEdges: ReadonlySet<string>;
  onSelectEdge: (from: string, to: string) => void;
  onOpenEdgeMenu: (
    edges: { from: string; to: string }[],
    x: number,
    y: number,
  ) => void;
  getWireVertices: (wireKey: string) => Point[];
  onWireVerticesChange: (wireKey: string, vertices: Point[]) => void;
  selectedVertex: { wireKey: string; index: number } | null;
  onSelectVertex: (wireKey: string, index: number) => void;
  obstacles: Rect[];
  onEditGate: (targetId: string, x: number, y: number) => void;
  onDeleteGate: (targetId: string) => void;
}) {
  const tx = centerX(gate.to);
  const cardTop = rowTop(gate.to.row);
  const targetCenter = { x: tx, y: cardTop + ROW_H / 2 };
  const sourceCenters = gate.parents.map((parent) => cardCenter(parent.from, centerX, rowTop));
  const sourceAverage =
    sourceCenters.length > 0
      ? averagePoint(sourceCenters)
      : { x: targetCenter.x, y: targetCenter.y - ROW_H };
  const sourceExits = gate.parents.map((parent) =>
    cardBoundary(parent.from, targetCenter, centerX, rowTop),
  );
  let axisStart =
    sourceExits.length > 0
      ? averagePoint(sourceExits)
      : { x: targetCenter.x, y: targetCenter.y - ROW_H };
  const axisEnd = cardBoundary(gate.to, sourceAverage, centerX, rowTop);
  if (Math.hypot(axisEnd.x - axisStart.x, axisEnd.y - axisStart.y) < 1) {
    axisStart = { x: axisEnd.x, y: axisEnd.y - ROW_H };
  }
  const storedGateRatio =
    typeof storedRatio === "number" && Number.isFinite(storedRatio)
      ? Math.max(0, Math.min(1, storedRatio))
      : DEFAULT_GATE_RATIO;
  const fallbackCenter = pointOnLine(
    axisStart,
    axisEnd,
    nearestClearRatio(
      axisStart,
      axisEnd,
      storedGateRatio,
      obstacles,
      GATE_W / 2 + WIRE_CLEARANCE,
      GATE_H / 2 + WIRE_CLEARANCE,
    ),
  );
  const initialCenter =
    storedPoint &&
    Number.isFinite(storedPoint.x) &&
    Number.isFinite(storedPoint.y)
      ? constrainPointOutsideRects(
          storedPoint,
          obstacles,
          Math.max(GATE_W, GATE_H) / 2 + WIRE_CLEARANCE,
        )
      : fallbackCenter;
  const [gateCenter, setGateCenter] = useState(initialCenter);
  const centerRef = useRef(initialCenter);
  const dragRef = useRef<{
    pointerId: number;
    start: Point;
    origin: Point;
    moved: boolean;
  } | null>(null);
  const [dragging, setDragging] = useState(false);
  const suppressClickRef = useRef(false);

  useEffect(() => {
    if (dragRef.current) return;
    centerRef.current = initialCenter;
    setGateCenter(initialCenter);
  }, [initialCenter.x, initialCenter.y]);

  const startGateDrag = (event: React.PointerEvent<SVGGElement>) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    const point = svgPointerPoint(event);
    if (!point) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      pointerId: event.pointerId,
      start: point,
      origin: centerRef.current,
      moved: false,
    };
    setDragging(true);
  };

  const moveGate = (event: React.PointerEvent<SVGGElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    const pointer = svgPointerPoint(event);
    if (!pointer) return;
    const next = constrainPointOutsideRects(
      {
        x: drag.origin.x + pointer.x - drag.start.x,
        y: drag.origin.y + pointer.y - drag.start.y,
      },
      obstacles,
      Math.max(GATE_W, GATE_H) / 2 + WIRE_CLEARANCE,
    );
    centerRef.current = next;
    setGateCenter(next);
    if (Math.hypot(pointer.x - drag.start.x, pointer.y - drag.start.y) > 3) {
      drag.moved = true;
    }
  };

  const finishGateDrag = (event: React.PointerEvent<SVGGElement>, commit: boolean) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    suppressClickRef.current = drag.moved;
    if (commit && drag.moved) {
      onPositionChange(gate.to.node.id, centerRef.current);
    } else if (!commit) {
      centerRef.current = initialCenter;
      setGateCenter(initialCenter);
    }
  };

  const resetGate = (event: React.MouseEvent<SVGGElement>) => {
    event.preventDefault();
    event.stopPropagation();
    centerRef.current = fallbackCenter;
    setGateCenter(fallbackCenter);
    onPositionChange(gate.to.node.id, fallbackCenter);
  };

  // A lone non-required parent has no boolean operator to show, so the gate
  // box states the relation instead.
  const effectiveOp =
    gate.op ??
    (gate.parents.length === 1 && gate.parents[0].relation !== ""
      ? gate.parents[0].relation
      : null);

  if (!effectiveOp || gate.parents.length === 0) {
    return (
      <>
        {gate.parents.map((parent, index) => {
          const sourceCenter = cardCenter(parent.from, centerX, rowTop);
          const edgeKey = edgePlacementKey(parent.from.node.id, gate.to.node.id);
          const vertices = getWireVertices(edgeKey);
          return (
            <DependencyLine
              key={index}
              start={cardBoundary(
                parent.from,
                vertices[0] ?? targetCenter,
                centerX,
                rowTop,
              )}
              end={cardBoundary(
                gate.to,
                vertices[vertices.length - 1] ?? sourceCenter,
                centerX,
                rowTop,
              )}
              vertices={vertices}
              edgeKey={edgeKey}
              line={parent.line}
              relation={parent.relation}
              muted={parent.muted}
              selected={selectedEdges.has(edgeKey)}
              onSelect={() => onSelectEdge(parent.from.node.id, gate.to.node.id)}
              onOpenStyleMenu={(x, y) =>
                onOpenEdgeMenu(
                  [{ from: parent.from.node.id, to: gate.to.node.id }],
                  x,
                  y,
                )
              }
              selectedVertexIndex={
                selectedVertex?.wireKey === edgeKey ? selectedVertex.index : undefined
              }
              onSelectVertex={(vertexIndex) => onSelectVertex(edgeKey, vertexIndex)}
              onVerticesChange={(next) => onWireVerticesChange(edgeKey, next)}
              obstacles={obstacles}
            />
          );
        })}
      </>
    );
  }

  const presentation = OP_PRESENTATION[effectiveOp] ?? {
    label: effectiveOp.toUpperCase(),
    description: effectiveOp.toUpperCase(),
  };
  // Binary operators are meaningless with a single incoming edge; flag them
  // so a dangling AND/OR reads as "wiring incomplete", not "condition ok".
  const gateInvalid =
    BINARY_GATE_OPS.has(effectiveOp) && gate.parents.length < 2;

  return (
    <g className={`gate gate-${effectiveOp}${gateInvalid ? " gate-invalid" : ""}`}>
      {gate.parents.map((parent, index) => {
        const sourceCenter = cardCenter(parent.from, centerX, rowTop);
        const edgeKey = edgePlacementKey(parent.from.node.id, gate.to.node.id);
        const vertices = getWireVertices(edgeKey);
        return (
          <DependencyLine
            key={index}
            start={cardBoundary(
              parent.from,
              vertices[0] ?? gateCenter,
              centerX,
              rowTop,
            )}
            end={rectBoundary(
              gateCenter,
              GATE_W / 2,
              GATE_H / 2,
              vertices[vertices.length - 1] ?? sourceCenter,
            )}
            vertices={vertices}
            edgeKey={edgeKey}
            line={parent.line}
            relation={parent.relation}
            muted={parent.muted}
            selected={selectedEdges.has(edgeKey)}
            onSelect={() => onSelectEdge(parent.from.node.id, gate.to.node.id)}
            onOpenStyleMenu={(x, y) =>
              onOpenEdgeMenu(
                [{ from: parent.from.node.id, to: gate.to.node.id }],
                x,
                y,
              )
            }
            selectedVertexIndex={
              selectedVertex?.wireKey === edgeKey ? selectedVertex.index : undefined
            }
            onSelectVertex={(vertexIndex) => onSelectVertex(edgeKey, vertexIndex)}
            onVerticesChange={(next) => onWireVerticesChange(edgeKey, next)}
            obstacles={obstacles}
          />
        );
      })}
      {(() => {
        const wireKey = gateWireKey(gate.to.node.id);
        const vertices = getWireVertices(wireKey);
        return (
          <DependencyLine
            start={rectBoundary(
              gateCenter,
              GATE_W / 2,
              GATE_H / 2,
              vertices[0] ?? targetCenter,
            )}
            end={cardBoundary(
              gate.to,
              vertices[vertices.length - 1] ?? gateCenter,
              centerX,
              rowTop,
            )}
            vertices={vertices}
            line={
              effectiveOp === "optional"
                ? "dashed"
                : effectiveOp === "deprecated"
                  ? "dotted"
                  : "solid"
            }
            relation={
              effectiveOp === "optional" || effectiveOp === "deprecated"
                ? effectiveOp
                : ""
            }
            // The gate's own wire fades once nothing live feeds it.
            muted={gate.parents.every((parent) => parent.muted)}
            selected={selectedVertex?.wireKey === wireKey}
            selectedVertexIndex={
              selectedVertex?.wireKey === wireKey ? selectedVertex.index : undefined
            }
            onSelectVertex={(vertexIndex) => onSelectVertex(wireKey, vertexIndex)}
            onVerticesChange={(next) => onWireVerticesChange(wireKey, next)}
            obstacles={obstacles}
            onOpenStyleMenu={(x, y) =>
              onOpenEdgeMenu(
                gate.parents.map((parent) => ({
                  from: parent.from.node.id,
                  to: gate.to.node.id,
                })),
                x,
                y,
              )
            }
          />
        );
      })()}
      <g
        className={`gate-handle${dragging ? " dragging" : ""}`}
        onPointerDown={startGateDrag}
        onPointerMove={moveGate}
        onPointerUp={(event) => finishGateDrag(event, true)}
        onPointerCancel={(event) => finishGateDrag(event, false)}
        onLostPointerCapture={(event) => finishGateDrag(event, true)}
        onClick={(event) => {
          if (suppressClickRef.current) {
            suppressClickRef.current = false;
            return;
          }
          event.preventDefault();
          event.stopPropagation();
          onEditGate(gate.to.node.id, event.clientX, event.clientY);
        }}
        onDoubleClick={resetGate}
      >
        <title>{`${presentation.description}。${
          gateInvalid
            ? `目前只有 ${gate.parents.length} 個輸入，${presentation.label} 需要至少 2 個前置節點。`
            : ""
        }點擊編輯，拖曳調整位置，雙擊重設。`}</title>
        <rect
          className="gate-node"
          x={gateCenter.x - GATE_W / 2}
          y={gateCenter.y - GATE_H / 2}
          width={GATE_W}
          height={GATE_H}
          rx={6}
        />
        <text className="gate-label" x={gateCenter.x} y={gateCenter.y} textAnchor="middle">
          {presentation.label}
        </text>
        {gateInvalid && (
          <g
            className="logic-gate-delete"
            role="button"
            aria-label="刪除條件閘"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              onDeleteGate(gate.to.node.id);
            }}
          >
            <title>輸入不足的條件閘。點擊刪除（清除此節點的條件，保留連線）。</title>
            <circle cx={gateCenter.x + GATE_W / 2} cy={gateCenter.y - GATE_H / 2} r={7} />
            <path
              d={`M ${gateCenter.x + GATE_W / 2 - 2.5} ${gateCenter.y - GATE_H / 2 - 2.5} l 5 5 M ${
                gateCenter.x + GATE_W / 2 + 2.5
              } ${gateCenter.y - GATE_H / 2 - 2.5} l -5 5`}
            />
          </g>
        )}
      </g>
    </g>
  );
}
