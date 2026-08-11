import { useRef, useState, type PointerEvent } from "react";
import { segmentRectInterval, type Point } from "./geometry";

type ClientRect = { left: number; right: number; top: number; bottom: number };

/**
 * Client-space points of one wire.
 *
 * The user-space points are mapped through the element's own box rather than
 * `getScreenCTM()`: the board scales with the CSS `zoom` property, which
 * `getBoundingClientRect()` accounts for and the SVG screen matrix does not.
 */
function wirePointsInClientSpace(path: SVGPolylineElement): Point[] {
  const box = path.getBBox();
  const rect = path.getBoundingClientRect();
  const scaleX = box.width > 0 ? rect.width / box.width : 1;
  const scaleY = box.height > 0 ? rect.height / box.height : 1;
  const offsetX = rect.left - box.x * scaleX;
  const offsetY = rect.top - box.y * scaleY;
  const points: Point[] = [];
  for (let index = 0; index < path.points.numberOfItems; index++) {
    const point = path.points.getItem(index);
    points.push({
      x: point.x * scaleX + offsetX,
      y: point.y * scaleY + offsetY,
    });
  }
  return points;
}

/**
 * Wire keys whose drawn path crosses the marquee. The check walks the wire's
 * own segments rather than its bounding box, because a diagonal wire's box
 * covers far more board than the line does.
 */
export function wiresInside(container: Element, rect: ClientRect): string[] {
  const found: string[] = [];
  for (const wire of container.querySelectorAll<SVGGElement>(
    ".dependency-line[data-edge-key]",
  )) {
    const key = wire.dataset.edgeKey;
    const path = wire.querySelector<SVGPolylineElement>("polyline.edge-wire");
    if (!key || !path) continue;
    // Cheap rejection first: most wires are nowhere near the marquee.
    const bounds = path.getBoundingClientRect();
    if (
      bounds.right < rect.left ||
      bounds.left > rect.right ||
      bounds.bottom < rect.top ||
      bounds.top > rect.bottom
    ) {
      continue;
    }
    const points = wirePointsInClientSpace(path);
    const crosses = points.some(
      (point, index) =>
        index > 0 && segmentRectInterval(points[index - 1], point, rect) !== null,
    );
    if (crosses) found.push(key);
  }
  return found;
}

export interface MarqueeDrag {
  pointerId: number;
  start: Point;
  current: Point;
  startClient: Point;
  currentClient: Point;
}

export function useMarquee({
  enabled,
  withoutModifier = false,
  pointFromClient,
  onSingleSelect,
  onMultiSelect,
  onSelectEdges,
  onClearSelection,
}: {
  enabled: boolean;
  /**
   * Start a marquee on a plain drag, for a pointer that cannot hold Ctrl/⌘.
   * Set by the board's gesture mode; the modifier keeps working either way.
   */
  withoutModifier?: boolean;
  pointFromClient: (clientX: number, clientY: number) => Point | null;
  onSingleSelect: (nodeId: string) => void;
  onMultiSelect: (nodeIds: string[]) => void;
  /** Called when the marquee caught wires but no cards. */
  onSelectEdges?: (wireKeys: string[]) => void;
  onClearSelection: () => void;
}) {
  const marqueeRef = useRef<MarqueeDrag | null>(null);
  const [state, setState] = useState<MarqueeDrag | null>(null);

  const onPointerDown = (event: PointerEvent<HTMLDivElement>): boolean => {
    if (!enabled) return false;
    if (!withoutModifier && !(event.ctrlKey || event.metaKey)) return false;
    const point = pointFromClient(event.clientX, event.clientY);
    if (!point) return false;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    const next: MarqueeDrag = {
      pointerId: event.pointerId,
      start: point,
      current: point,
      startClient: { x: event.clientX, y: event.clientY },
      currentClient: { x: event.clientX, y: event.clientY },
    };
    marqueeRef.current = next;
    setState(next);
    onClearSelection();
    return true;
  };

  const onPointerMove = (event: PointerEvent<HTMLDivElement>): boolean => {
    const current = marqueeRef.current;
    if (!current || current.pointerId !== event.pointerId) return false;
    const point = pointFromClient(event.clientX, event.clientY);
    if (!point) return true;
    const next = {
      ...current,
      current: point,
      currentClient: { x: event.clientX, y: event.clientY },
    };
    marqueeRef.current = next;
    setState(next);
    return true;
  };

  const onPointerUp = (event: PointerEvent<HTMLDivElement>): boolean => {
    const current = marqueeRef.current;
    if (!current || current.pointerId !== event.pointerId) return false;
    const left = Math.min(current.startClient.x, event.clientX);
    const right = Math.max(current.startClient.x, event.clientX);
    const top = Math.min(current.startClient.y, event.clientY);
    const bottom = Math.max(current.startClient.y, event.clientY);
    const moved = right - left > 3 || bottom - top > 3;
    const nodeIds = moved
      ? Array.from(event.currentTarget.querySelectorAll<HTMLElement>(".col-card[data-node-id]")).flatMap(
          (card) => {
            const rect = card.getBoundingClientRect();
            const intersects =
              rect.right >= left &&
              rect.left <= right &&
              rect.bottom >= top &&
              rect.top <= bottom;
            return intersects && card.dataset.nodeId ? [card.dataset.nodeId] : [];
          },
        )
      : [];
    marqueeRef.current = null;
    setState(null);
    // Cards win the selection; a marquee over empty board that only crosses
    // wires selects a wire instead of clearing everything.
    const wireKeys =
      moved && nodeIds.length === 0 && onSelectEdges
        ? wiresInside(event.currentTarget, { left, right, top, bottom })
        : [];
    if (nodeIds.length === 1) onSingleSelect(nodeIds[0]);
    else if (nodeIds.length > 1) onMultiSelect(nodeIds);
    else if (wireKeys.length && onSelectEdges) onSelectEdges(wireKeys);
    else onMultiSelect(nodeIds);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    return true;
  };

  const cancel = () => {
    if (!marqueeRef.current) return false;
    marqueeRef.current = null;
    setState(null);
    return true;
  };

  return {
    state,
    handlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp,
      onPointerCancel: onPointerUp,
      cancel,
    },
  };
}
