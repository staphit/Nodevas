/**
 * One dependency wire [B-06].
 *
 * Routes around card obstacles, carries user-placed bend points, and exposes a
 * fat invisible hit target so the line is clickable without being thick.
 */

import { useState, useEffect, useRef } from "react";
import type { EdgeLine, EdgeRelation } from "../../types";

import {
  closestPointOnPolyline,
  constrainPointOutsideRects,
  svgPointerPoint,
  type Point,
  type Rect,
} from "./geometry";
import { useGraphGestureMode } from "./gestureMode";
import { useI18n } from "../../i18n";



export function DependencyLine({
  start,
  end,
  vertices = [],
  edgeKey,
  line = "solid",
  relation = "",
  muted = false,
  selected = false,
  onSelect,
  onOpenStyleMenu,
  selectedVertexIndex,
  onSelectVertex,
  onVerticesChange,
  obstacles = [],
}: {
  start: Point;
  end: Point;
  vertices?: Point[];
  /** Placement key of the wire, so a marquee can pick it up. */
  edgeKey?: string;
  /** How the wire is drawn. */
  line?: Exclude<EdgeLine, "">;
  /** What the wire means; only used to tint a deprecated relation. */
  relation?: EdgeRelation;
  /** Draw faded regardless of the relation: the source node is out of play. */
  muted?: boolean;
  selected?: boolean;
  onSelect?: () => void;
  onOpenStyleMenu?: (x: number, y: number) => void;
  selectedVertexIndex?: number;
  onSelectVertex?: (index: number) => void;
  onVerticesChange?: (vertices: Point[]) => void;
  obstacles?: Rect[];
}) {
  const { t } = useI18n();
  const vertexSignature = vertices.map((point) => `${point.x},${point.y}`).join(";");
  const [workingVertices, setWorkingVertices] = useState(vertices);
  const workingVerticesRef = useRef(vertices);
  const [preview, setPreview] = useState<{ point: Point; segmentIndex: number } | null>(
    null,
  );
  const vertexDragRef = useRef<{
    pointerId: number;
    index: number;
    original: Point[];
    moved: boolean;
  } | null>(null);

  // Alt+click inserts a bend for a mouse. A finger has no Alt, so the board's
  // connect mode says the same thing for the whole board at once.
  const gestureMode = useGraphGestureMode();
  const insertingVertex = (event: { altKey: boolean }) =>
    event.altKey || gestureMode === "connect";

  useEffect(() => {
    if (vertexDragRef.current) return;
    workingVerticesRef.current = vertices;
    setWorkingVertices(vertices);
  }, [vertexSignature]);

  const displayedVertices = workingVertices.map((point) =>
    constrainPointOutsideRects(point, obstacles),
  );
  const pathPoints = [start, ...displayedVertices, end];
  const serializedPoints = pathPoints.map((point) => `${point.x},${point.y}`).join(" ");

  const previewAtPointer = (event: React.PointerEvent<SVGPolylineElement>) => {
    if (!insertingVertex(event) || !onVerticesChange || workingVertices.length >= 64) {
      setPreview(null);
      return null;
    }
    const rawPointer = svgPointerPoint(event);
    const pointer = rawPointer
      ? constrainPointOutsideRects(rawPointer, obstacles)
      : null;
    if (!pointer) return null;
    const next = closestPointOnPolyline(pathPoints, pointer);
    setPreview(next);
    return next;
  };

  const startVertexDrag = (
    event: React.PointerEvent<SVGCircleElement>,
    index: number,
  ) => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    vertexDragRef.current = {
      pointerId: event.pointerId,
      index,
      original: workingVertices.map((point) => ({ ...point })),
      moved: false,
    };
    onSelectVertex?.(index);
  };

  const moveVertex = (event: React.PointerEvent<SVGCircleElement>) => {
    const drag = vertexDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const rawPoint = svgPointerPoint(event);
    if (!rawPoint) return;
    const point = constrainPointOutsideRects(rawPoint, obstacles);
    event.preventDefault();
    event.stopPropagation();
    drag.moved = true;
    const next = workingVerticesRef.current.map((vertex, index) =>
      index === drag.index ? point : vertex,
    );
    workingVerticesRef.current = next;
    setWorkingVertices(next);
  };

  const finishVertexDrag = (
    event: React.PointerEvent<SVGCircleElement>,
    commit: boolean,
  ) => {
    const drag = vertexDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    vertexDragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (!commit) {
      workingVerticesRef.current = drag.original;
      setWorkingVertices(drag.original);
      return;
    }
    if (drag.moved) onVerticesChange?.(workingVerticesRef.current);
  };

  const removeVertex = (event: React.MouseEvent<SVGCircleElement>, index: number) => {
    event.preventDefault();
    event.stopPropagation();
    const next = workingVerticesRef.current.filter(
      (_, vertexIndex) => vertexIndex !== index,
    );
    workingVerticesRef.current = next;
    setWorkingVertices(next);
    onVerticesChange?.(next);
  };

  return (
    <g
      className={`dependency-line${selected ? " selected" : ""}${
        preview ? " inserting-vertex" : ""
      }`}
      data-edge-key={edgeKey}
    >
      <title>
        {gestureMode === "connect"
          ? t("canvas.edge.insertHint")
          : t("canvas.edge.selectHint")}
      </title>
      {(onSelect || onVerticesChange) && (
        <polyline
          className="edge-hit-target"
          points={serializedPoints}
          onPointerMove={previewAtPointer}
          onPointerLeave={() => setPreview(null)}
          onPointerDown={(event) => {
            if (event.pointerType === "mouse" && event.button !== 0) return;
            event.preventDefault();
            event.stopPropagation();
            if (insertingVertex(event) && onVerticesChange && workingVertices.length < 64) {
              const insertion = previewAtPointer(event);
              if (!insertion) return;
              const next = [...workingVerticesRef.current];
              next.splice(insertion.segmentIndex, 0, insertion.point);
              workingVerticesRef.current = next;
              setWorkingVertices(next);
              onVerticesChange(next);
              setPreview(null);
              return;
            }
            onSelect?.();
          }}
          onContextMenu={(event) => {
            if (!onOpenStyleMenu) return;
            event.preventDefault();
            event.stopPropagation();
            onSelect?.();
            onOpenStyleMenu(event.clientX, event.clientY);
          }}
        />
      )}
      <polyline
        className={`edge-wire line-${line}${relation ? ` relation-${relation}` : ""}${
          muted ? " muted" : ""
        }`}
        points={serializedPoints}
        markerEnd={
          relation === "deprecated" || muted
            ? "url(#arrowhead-muted)"
            : "url(#arrowhead)"
        }
      />
      {displayedVertices.map((vertex, index) => (
        // The drawn dot stays small; the handle around it is what the pointer
        // has to hit, so a bend point can be grabbed and deleted without
        // pixel-hunting.
        <g key={index} className="edge-vertex-handle">
          <circle
            className="edge-vertex-hit"
            cx={vertex.x}
            cy={vertex.y}
            r={11}
            onPointerDown={(event) => startVertexDrag(event, index)}
            onPointerMove={moveVertex}
            onPointerUp={(event) => finishVertexDrag(event, true)}
            onPointerCancel={(event) => finishVertexDrag(event, false)}
            onLostPointerCapture={(event) => finishVertexDrag(event, true)}
            onDoubleClick={(event) => removeVertex(event, index)}
            onContextMenu={(event) => removeVertex(event, index)}
          >
            <title>{t("canvas.edge.vertexHint")}</title>
          </circle>
          <circle
            className={`edge-vertex${
              selectedVertexIndex === index ? " selected" : ""
            }`}
            cx={vertex.x}
            cy={vertex.y}
            r={4.5}
          />
        </g>
      ))}
      {preview && (
        <circle
          className="edge-vertex-preview"
          cx={preview.point.x}
          cy={preview.point.y}
          r={5}
        />
      )}
    </g>
  );
}
