/**
 * First-class logic gates on the canvas [B-06].
 *
 * The glyph and its wiring UI. Gate semantics (what an operator means, when a
 * gate is complete) come from the domain layer.
 */

import { useState, useEffect, useRef } from "react";
import {
  logicGateInputRequirement,
  logicGateInputWireKey,
  logicGateIsComplete,
  logicGateOutputs,
  logicGateOutputWireKey,
  logicGateRelation,
} from "../../domain";
import { DEFAULT_LINE } from "../../domain/graph/edgeStyle";
import type { LogicGate, LogicGateOperator } from "../../types";
import { useI18n } from "../../i18n";
import { DependencyLine } from "./DependencyLine";
import {
  cardBoundary,
  cardCenter,
  rectBoundary,
  svgPointerPoint,
  type Col,
  type Point,
  type Rect,
} from "./geometry";



// ---------- first-class logic gates ----------

export const LOGIC_GATE_W = 72;
export const LOGIC_GATE_H = 46;

// The wire keys are part of the file contract; they live in `domain/graph/keys`.
export { logicGateInputWireKey, logicGateOutputWireKey };

export function LogicGateGlyph({
  operator,
}: {
  operator: LogicGateOperator;
}) {
  const { t } = useI18n();
  return (
    <>
      <rect
        className="logic-gate-shape"
        x={-LOGIC_GATE_W / 2}
        y={-12}
        width={LOGIC_GATE_W}
        height={24}
        rx={6}
      />
      <text className="logic-gate-symbol-label" x={0} y={0}>
        {t(`gate.label.${operator}`)}
      </text>
    </>
  );
}

export function StandaloneLogicGate({
  gate,
  center,
  inputCols,
  outputCols,
  centerX,
  rowTop,
  selected,
  connectionSource,
  connectionTarget,
  connectionTargetInvalid,
  onSelect,
  onOpenMenu,
  onDelete,
  onMove,
  onStartConnection,
  onConnectionMove,
  onConnectionEnd,
  getWireVertices,
  onWireVerticesChange,
  selectedVertex,
  onSelectVertex,
  obstacles,
}: {
  gate: LogicGate;
  center: Point;
  inputCols: Col[];
  /** One per output node; a relation gate drives several at once. */
  outputCols: Col[];
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  selected: boolean;
  connectionSource: boolean;
  connectionTarget: boolean;
  connectionTargetInvalid: boolean;
  onSelect: () => void;
  onOpenMenu: (x: number, y: number) => void;
  onDelete: () => void;
  onMove: (point: Point) => void;
  onStartConnection: (event: React.PointerEvent<SVGGElement>, start: Point) => boolean;
  onConnectionMove: (event: React.PointerEvent) => boolean;
  onConnectionEnd: (event: React.PointerEvent, commit: boolean) => boolean;
  getWireVertices: (wireKey: string) => Point[];
  onWireVerticesChange: (wireKey: string, vertices: Point[]) => void;
  selectedVertex: { wireKey: string; index: number } | null;
  onSelectVertex: (wireKey: string, index: number) => void;
  obstacles: Rect[];
}) {
  const { t } = useI18n();
  const [displayCenter, setDisplayCenter] = useState(center);
  const dragRef = useRef<{
    pointerId: number;
    start: Point;
    origin: Point;
    moved: boolean;
  } | null>(null);

  useEffect(() => {
    if (!dragRef.current) setDisplayCenter(center);
  }, [center.x, center.y]);

  const complete = logicGateIsComplete(gate);
  const required = logicGateInputRequirement(gate.operator);
  const outputs = logicGateOutputs(gate);
  const relation = logicGateRelation(gate.operator);
  const title = t("logicGate.aria", {
    label: t(`gate.label.${gate.operator}`),
    inputs: `${gate.inputs.length}/${gate.operator === "must" ? required : `${required}+`}`,
    outputs: outputs.length,
    state: complete
      ? relation
        ? t("logicGate.state.relationActive")
        : t("logicGate.state.conditionActive")
      : t("logicGate.state.incomplete"),
  });

  const finishDrag = (event: React.PointerEvent<SVGGElement>, commit: boolean) => {
    if (onConnectionEnd(event, commit)) return;
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (commit && drag.moved) onMove(displayCenter);
    else setDisplayCenter(center);
  };

  return (
    <g>
      {inputCols.map((column) => {
        const sourceCenter = cardCenter(column, centerX, rowTop);
        const wireKey = logicGateInputWireKey(gate.id, column.node.id);
        const vertices = getWireVertices(wireKey);
        return (
          <DependencyLine
            key={wireKey}
            start={cardBoundary(
              column,
              vertices[0] ?? displayCenter,
              centerX,
              rowTop,
            )}
            end={rectBoundary(
              displayCenter,
              LOGIC_GATE_W / 2,
              LOGIC_GATE_H / 2,
              vertices[vertices.length - 1] ?? sourceCenter,
            )}
            vertices={vertices}
            // A relation gate's wires are the relation's own edges, so they
            // are drawn the way that relation is drawn everywhere else.
            relation={relation ?? undefined}
            line={relation ? DEFAULT_LINE[relation] : undefined}
            selected={selectedVertex?.wireKey === wireKey}
            selectedVertexIndex={
              selectedVertex?.wireKey === wireKey ? selectedVertex.index : undefined
            }
            onSelectVertex={(index) => onSelectVertex(wireKey, index)}
            onVerticesChange={(vertices) => onWireVerticesChange(wireKey, vertices)}
            obstacles={obstacles}
          />
        );
      })}
      {outputCols.map((column) => {
        const outputCenter = cardCenter(column, centerX, rowTop);
        // A boolean gate has one output and keeps the unsuffixed key its bend
        // points were saved under.
        const wireKey = logicGateOutputWireKey(
          gate.id,
          relation ? column.node.id : undefined,
        );
        const vertices = getWireVertices(wireKey);
        return (
          <DependencyLine
            key={wireKey}
            start={rectBoundary(
              displayCenter,
              LOGIC_GATE_W / 2,
              LOGIC_GATE_H / 2,
              vertices[0] ?? outputCenter,
            )}
            end={cardBoundary(
              column,
              vertices[vertices.length - 1] ?? displayCenter,
              centerX,
              rowTop,
            )}
            vertices={vertices}
            relation={relation ?? undefined}
            line={relation ? DEFAULT_LINE[relation] : undefined}
            selected={selectedVertex?.wireKey === wireKey}
            selectedVertexIndex={
              selectedVertex?.wireKey === wireKey ? selectedVertex.index : undefined
            }
            onSelectVertex={(index) => onSelectVertex(wireKey, index)}
            onVerticesChange={(vertices) => onWireVerticesChange(wireKey, vertices)}
            obstacles={obstacles}
          />
        );
      })}
      <g
        data-gate-id={gate.id}
        className={`logic-gate gate-handle gate-${gate.operator}${
          complete ? " complete" : " invalid"
        }${selected ? " selected" : ""}${connectionSource ? " connection-source" : ""}${
          connectionTarget
            ? connectionTargetInvalid
              ? " connection-target invalid"
              : " connection-target valid"
            : ""
        }`}
        transform={`translate(${displayCenter.x} ${displayCenter.y})`}
        role="button"
        tabIndex={0}
        aria-label={title}
        onContextMenu={(event) => {
          event.preventDefault();
          event.stopPropagation();
          onOpenMenu(event.clientX, event.clientY);
        }}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          if (event.altKey) {
            onStartConnection(event, {
              x: displayCenter.x + LOGIC_GATE_W / 2,
              y: displayCenter.y,
            });
            return;
          }
          event.preventDefault();
          event.stopPropagation();
          onSelect();
          const point = svgPointerPoint(event);
          if (!point) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          dragRef.current = {
            pointerId: event.pointerId,
            start: point,
            origin: displayCenter,
            moved: false,
          };
        }}
        onPointerMove={(event) => {
          if (onConnectionMove(event)) return;
          const drag = dragRef.current;
          if (!drag || drag.pointerId !== event.pointerId) return;
          const point = svgPointerPoint(event);
          if (!point) return;
          const next = {
            x: drag.origin.x + point.x - drag.start.x,
            y: drag.origin.y + point.y - drag.start.y,
          };
          drag.moved ||= Math.hypot(next.x - drag.origin.x, next.y - drag.origin.y) > 3;
          setDisplayCenter(next);
        }}
        onPointerUp={(event) => finishDrag(event, true)}
        onPointerCancel={(event) => finishDrag(event, false)}
        onLostPointerCapture={(event) => finishDrag(event, true)}
        onClick={(event) => {
          event.stopPropagation();
          onSelect();
        }}
      >
        <title>{title}</title>
        <LogicGateGlyph operator={gate.operator} />
        <text className="logic-gate-count" x={0} y={24}>
          {gate.inputs.length}/{gate.operator === "must" ? required : `${required}+`}
          {` → ${outputs.length}`}
        </text>
        {!complete && (
          <g
            className="logic-gate-delete"
            role="button"
            aria-label={t("logicGate.deleteIncompleteAria")}
            onPointerDown={(event) => event.stopPropagation()}
            onClick={(event) => {
              event.stopPropagation();
              onDelete();
            }}
          >
            <title>
              {t("logicGate.incompleteTitle", {
                required: `${required}${gate.operator === "must" ? "" : "+"}`,
              })}
            </title>
            <circle cx={LOGIC_GATE_W / 2 + 2} cy={-14} r={7} />
            <path d="M 35.5 -16.5 l 5 5 M 40.5 -16.5 l -5 5" />
          </g>
        )}
      </g>
    </g>
  );
}
