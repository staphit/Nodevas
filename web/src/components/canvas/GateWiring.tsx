/**
 * Inline dependency gate [B-06].
 *
 * Drawn in front of a node that has a `requires` expression but no first-class
 * logic gate. It renders the ordinary dependency fan-in that remains outside
 * first-class gates.
 */

import { memo, useState, useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import {
  ROW_H,
  WIRE_CLEARANCE,
  averagePoint,
  cardBoundary,
  cardCenter,
  constrainPointOutsideRects,
  edgePlacementKey,
  gateWireKey,
  pointOnLine,
  rectBoundary,
  svgPointerPoint,
  widestOpeningRatio,
  type Col,
  type Point,
  type Rect,
} from "./geometry";
import type { EdgeLine, EdgeRelation } from "../../types";
import { DependencyLine } from "./DependencyLine";
import { useI18n } from "../../i18n";
import { isPlainAllOfRequirement } from "./simpleDependency";
import {
  constrainPointOutsideIndex,
  nearestClearRatioInIndex,
  obstacleIndex,
  type ObstacleIndex,
} from "./obstacleIndex";



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

/**
 * The label a gate box shows. A box with no room for the whole of it names
 * just the operator ("OR" out of "Any · OR"), the part that tells one
 * condition from another; the full wording stays in its tooltip.
 */
export function gateBoxLabel(label: string, compact: boolean): string {
  if (!compact) return label;
  const at = label.lastIndexOf(" · ");
  return at >= 0 ? label.slice(at + 3) : label;
}

/** How wide a box drawn around `label` is: GATE_W, or just its text when compact. */
export function gateBoxWidth(label: string, compact: boolean): number {
  if (!compact) return GATE_W;
  // The label is 9.5px bold monospace: about 6px a Latin letter, a full em a
  // CJK character, plus a little padding either side.
  let text = 0;
  for (const char of label) text += char.charCodeAt(0) >= 0x2e80 ? 9.5 : 6;
  return Math.max(GATE_H, Math.ceil(text + 10));
}

export const BINARY_GATE_OPS = new Set(["and", "or", "xor", "nand", "nor"]);

/**
 * The operator the gate box would show. A lone non-required parent has no
 * boolean operator, so the box states the relation instead.
 */
export function effectiveGateOp(gate: Gate): string | null {
  return (
    gate.op ??
    (gate.parents.length === 1 && gate.parents[0].relation !== ""
      ? gate.parents[0].relation
      : null)
  );
}

/**
 * Whether the fan-in is drawn through a labelled gate box. The default
 * "depends on all of these" fan-in — required wires joined by a plain `and` —
 * is drawn as ordinary wires, so the common case carries no badge. Every other
 * condition keeps its box, and so does an AND left with too few inputs, which
 * needs the box to flag and delete it.
 */
export function showsGateBadge(gate: Gate): boolean {
  const op = effectiveGateOp(gate);
  if (!op || gate.parents.length === 0) return false;
  const defaultAllOf =
    op === "and" &&
    gate.parents.length >= 2 &&
    gate.parents.every((parent) => parent.relation === "") &&
    isPlainAllOfRequirement(gate.to.node.requires);
  return !defaultAllOf;
}

/** Where a gate's condition box sits, in board units. */
export interface GateLayout {
  /** Where the box is drawn: its hand placement pushed off any card, or else
      the fallback. */
  center: Point;
  /** Where the box goes without a placement — on the line from its inputs to
      its node, clear of the cards. A double-click puts it back here. */
  fallback: Point;
  /** No room for the whole box where it is drawn, so it shrinks to the
      operator alone: between two neighbouring columns there is only the
      gap between the cards. */
  compact: boolean;
}

/** Whether a box this size centred on `point` overlaps none of the cards. */
function boxIsClear(point: Point, halfWidth: number, halfHeight: number, obstacles: ObstacleIndex) {
  const box = {
    left: point.x - halfWidth,
    right: point.x + halfWidth,
    top: point.y - halfHeight,
    bottom: point.y + halfHeight,
  };
  return obstacles
    .near(box)
    .every(
      (rect) =>
        rect.left >= box.right ||
        rect.right <= box.left ||
        rect.top >= box.bottom ||
        rect.bottom <= box.top,
    );
}

/**
 * Lays out a gate's box against the board's obstacles. A gate drawn as plain
 * wires has no box, so nothing is routed for it.
 */
export function gateLayout(
  gate: Gate,
  centerX: (c: Col) => number,
  rowTop: (row: number) => number,
  storedPoint: Point | undefined,
  storedRatio: number | undefined,
  obstacles: ObstacleIndex,
): GateLayout {
  const tx = centerX(gate.to);
  const cardTop = rowTop(gate.to.row);
  const targetCenter = { x: tx, y: cardTop + ROW_H / 2 };
  if (!showsGateBadge(gate)) {
    return { center: targetCenter, fallback: targetCenter, compact: false };
  }
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
  let fallback = pointOnLine(
    axisStart,
    axisEnd,
    nearestClearRatioInIndex(
      axisStart,
      axisEnd,
      storedGateRatio,
      obstacles,
      GATE_W / 2 + WIRE_CLEARANCE,
      GATE_H / 2 + WIRE_CLEARANCE,
    ),
  );
  if (!boxIsClear(fallback, GATE_W / 2, GATE_H / 2, obstacles)) {
    // Nowhere on the line holds the whole box: centre it in the widest stretch
    // the cards leave, so as little of it as can be is over them.
    const opening = widestOpeningRatio(
      axisStart,
      axisEnd,
      obstacles.near({
        left: Math.min(axisStart.x, axisEnd.x),
        right: Math.max(axisStart.x, axisEnd.x),
        top: Math.min(axisStart.y, axisEnd.y),
        bottom: Math.max(axisStart.y, axisEnd.y),
      }),
    );
    if (opening !== null) fallback = pointOnLine(axisStart, axisEnd, opening);
  }
  const center =
    storedPoint &&
    Number.isFinite(storedPoint.x) &&
    Number.isFinite(storedPoint.y)
      ? constrainPointOutsideIndex(
          storedPoint,
          obstacles,
          Math.max(GATE_W, GATE_H) / 2 + WIRE_CLEARANCE,
        )
      : fallback;
  return { center, fallback, compact: !boxIsClear(center, GATE_W / 2, GATE_H / 2, obstacles) };
}

/**
 * Memoized: on a large board a pan that moves the culling window re-renders
 * the canvas, and a gate that stays on screen with the same layout, handlers
 * and selection is left alone. The board hands it one layout object per
 * placement — a new one whenever its cards, placement, bends or the cards
 * around it move — and handlers that keep their identity.
 */
export const GateWiring = memo(function GateWiring({
  gate,
  centerX,
  rowTop,
  layout,
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
  badgeLayer,
}: {
  gate: Gate;
  centerX: (c: Col) => number;
  rowTop: (row: number) => number;
  /** The box's place, worked out by the board; left out, it is worked out
      here from the stored placement and ratio. */
  layout?: GateLayout;
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
  /** What a dragged box or bend is kept off: every card on the board. */
  obstacles: Rect[];
  onEditGate: (targetId: string, x: number, y: number) => void;
  onDeleteGate: (targetId: string) => void;
  /** Where the box itself is drawn: a layer over the cards, so a box with no
      room clear of them still reads. Left out, it is drawn with its wires. */
  badgeLayer?: SVGGElement | null;
}) {
  const { t } = useI18n();
  const tx = centerX(gate.to);
  const cardTop = rowTop(gate.to.row);
  const targetCenter = { x: tx, y: cardTop + ROW_H / 2 };
  const {
    center: initialCenter,
    fallback: fallbackCenter,
    compact,
  } = layout ??
    gateLayout(gate, centerX, rowTop, storedPoint, storedRatio, obstacleIndex(obstacles));
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

  const effectiveOp = effectiveGateOp(gate);
  const placeBadge = (badge: React.ReactElement) =>
    badgeLayer ? createPortal(badge, badgeLayer) : badge;

  if (!effectiveOp || !showsGateBadge(gate)) {
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

  const presentation = {
    label: t(`gate.label.${effectiveOp}`),
    description: t(`gate.description.${effectiveOp}`),
  };
  const boxLabel = gateBoxLabel(presentation.label, compact);
  const boxW = gateBoxWidth(boxLabel, compact);
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
              boxW / 2,
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
              boxW / 2,
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
      {placeBadge(
        <g
          className={`gate-badge gate-${effectiveOp}${gateInvalid ? " gate-invalid" : ""}`}
          data-gate-for={gate.to.node.id}
        >
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
            <title>
              {[compact ? presentation.label : "",
                presentation.description,
                gateInvalid
                  ? t("gate.invalid", {
                      count: gate.parents.length,
                      operator: presentation.label,
                    })
                  : "",
                t("gate.interactionHint")]
                .filter(Boolean)
                .join(" ")}
            </title>
            <rect
              className="gate-node"
              x={gateCenter.x - boxW / 2}
              y={gateCenter.y - GATE_H / 2}
              width={boxW}
              height={GATE_H}
              rx={6}
            />
            <text className="gate-label" x={gateCenter.x} y={gateCenter.y} textAnchor="middle">
              {boxLabel}
            </text>
            {gateInvalid && (
              <g
                className="logic-gate-delete"
                role="button"
                aria-label={t("gate.deleteAria")}
                onPointerDown={(event) => event.stopPropagation()}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  onDeleteGate(gate.to.node.id);
                }}
              >
                <title>{t("gate.deleteTitle")}</title>
                <circle cx={gateCenter.x + boxW / 2} cy={gateCenter.y - GATE_H / 2} r={7} />
                <path
                  d={`M ${gateCenter.x + boxW / 2 - 2.5} ${gateCenter.y - GATE_H / 2 - 2.5} l 5 5 M ${
                    gateCenter.x + boxW / 2 + 2.5
                  } ${gateCenter.y - GATE_H / 2 - 2.5} l -5 5`}
                />
              </g>
            )}
          </g>
        </g>,
      )}
    </g>
  );
});
