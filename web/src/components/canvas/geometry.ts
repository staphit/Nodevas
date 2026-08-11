/**
 * Canvas geometry [B-06].
 *
 * Grid constants and the pure maths the graph canvas draws with: card
 * boundaries, wire routing around obstacles, pointer↔SVG conversion. No React,
 * no store — everything here is testable in isolation.
 */

import type { GraphNode } from "../../types";

// Grid: one column per node slot, one row per lane.
export const COL_W = 164;
export const ROW_H = 80;
export const CARD_GAP_X = 12;
export const CARD_GAP_Y = 12;
export const CARD_W = COL_W - CARD_GAP_X;
export const CARD_H = ROW_H - CARD_GAP_Y;
export const CARD_MIN_W = 120;
export const CARD_MAX_W = 360;
export const CARD_MIN_H = 52;
export const CARD_MAX_H = 180;

/**
 * Snap one axis of a drag onto the grid, in cell units.
 *
 * The delta is rounded against the card's own starting cell rather than on its
 * own, so a card that already sits off-lattice lands on the lattice instead of
 * carrying its old offset along. Returns the delta to apply, not the position,
 * because a multi-card drag moves the whole selection by the delta the card
 * under the pointer resolved to — the group keeps its shape.
 */
export function snapDragDelta(startCell: number, delta: number): number {
  return Math.round(startCell + delta) - startCell;
}

/** Eight handles like a diagram editor: corners resize both axes. */
export const CARD_HANDLES = [
  { name: 'nw', label: '左上', dirX: -1, dirY: -1 },
  { name: 'n', label: '上', dirX: 0, dirY: -1 },
  { name: 'ne', label: '右上', dirX: 1, dirY: -1 },
  { name: 'e', label: '右', dirX: 1, dirY: 0 },
  { name: 'se', label: '右下', dirX: 1, dirY: 1 },
  { name: 's', label: '下', dirX: 0, dirY: 1 },
  { name: 'sw', label: '左下', dirX: -1, dirY: 1 },
  { name: 'w', label: '左', dirX: -1, dirY: 0 },
] as const;

export const MIN_ZOOM = 0.5;
export const MAX_ZOOM = 2;
export const ZOOM_STEP = 0.1;
/** Space wires keep from a card edge, so a line never touches the box. */
export const WIRE_CLEARANCE = 6;

/** One node placed on the grid. */
export interface Col {
  node: GraphNode;
  row: number;
  index: number;
  width: number;
  height: number;
}

export interface Point {
  x: number;
  y: number;
}

export interface Rect {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export function dayKey(date: Date): string {
  return (
    date.getFullYear() +
    '-' +
    String(date.getMonth() + 1).padStart(2, '0') +
    '-' +
    String(date.getDate()).padStart(2, '0')
  );
}

export function parseLocalDay(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return dayKey(date) === value ? date : null;
}

export function addLocalDays(date: Date, amount: number): Date {
  const next = new Date(date);
  next.setDate(next.getDate() + amount);
  next.setHours(0, 0, 0, 0);
  return next;
}

export function edgeKeyEndpoints(wireKey: string): { from: string; to: string } | null {
  if (wireKey.startsWith('gate:') || wireKey.startsWith('logic:')) return null;
  const separator = wireKey.indexOf('->');
  if (separator <= 0 || separator >= wireKey.length - 2) return null;
  return {
    from: wireKey.slice(0, separator),
    to: wireKey.slice(separator + 2),
  };
}

export function clampCardWidth(width: number): number {
  return Math.round(Math.max(CARD_MIN_W, Math.min(CARD_MAX_W, width)));
}

export function clampCardHeight(height: number): number {
  return Math.round(Math.max(CARD_MIN_H, Math.min(CARD_MAX_H, height)));
}

export function resizeCardSize(
  startWidth: number,
  startHeight: number,
  dirX: number,
  dirY: number,
  clientDeltaX: number,
  clientDeltaY: number,
  zoom: number,
): { width: number; height: number } {
  const scale = Math.max(zoom, 0.01);
  const dx = (clientDeltaX / scale) * dirX * 2;
  const dy = (clientDeltaY / scale) * dirY * 2;
  return {
    width: dirX ? clampCardWidth(startWidth + dx) : startWidth,
    height: dirY ? clampCardHeight(startHeight + dy) : startHeight,
  };
}

export function resizeCardByKeyboard(
  width: number,
  height: number,
  key: string,
  shiftKey = false,
): { width: number; height: number } | null {
  const step = shiftKey ? 24 : 8;
  if (key === 'ArrowLeft') return { width: clampCardWidth(width - step), height };
  if (key === 'ArrowRight') return { width: clampCardWidth(width + step), height };
  if (key === 'ArrowUp') return { width, height: clampCardHeight(height - step) };
  if (key === 'ArrowDown') return { width, height: clampCardHeight(height + step) };
  return null;
}

export interface CanvasLayout {
  isGraph: boolean;
  graphOffsetX: number;
  graphOffsetY: number;
  timelineCellWidth: number;
  dayY: number[];
  dayCount: number;
}

export function centerX(column: Col, layout: CanvasLayout): number {
  const cellWidth = layout.isGraph ? COL_W : layout.timelineCellWidth;
  return (layout.isGraph ? layout.graphOffsetX : 0) + column.index * cellWidth + cellWidth / 2;
}

export function rowTop(row: number, layout: CanvasLayout): number {
  if (layout.isGraph) return layout.graphOffsetY + row * ROW_H;
  return layout.dayY[Math.max(0, Math.min(layout.dayCount - 1, row))] ?? 0;
}

export function clampZoom(zoom: number): number {
  return Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, Math.round(zoom * 10) / 10));
}

export function contentPointFromClient(
  clientX: number,
  clientY: number,
  contentRect: Pick<DOMRect, 'left' | 'top'>,
  zoom: number,
): Point {
  return {
    x: (clientX - contentRect.left) / zoom,
    y: (clientY - contentRect.top) / zoom,
  };
}

export function expandRect(rect: Rect, horizontal: number, vertical: number): Rect {
  return {
    left: rect.left - horizontal,
    right: rect.right + horizontal,
    top: rect.top - vertical,
    bottom: rect.bottom + vertical,
  };
}

export function pointInsideRect(point: Point, rect: Rect): boolean {
  return (
    point.x >= rect.left &&
    point.x <= rect.right &&
    point.y >= rect.top &&
    point.y <= rect.bottom
  );
}

export function constrainPointOutsideRects(
  point: Point,
  obstacles: Rect[],
  clearance = WIRE_CLEARANCE,
): Point {
  let next = { ...point };
  for (let pass = 0; pass < obstacles.length; pass++) {
    const obstacle = obstacles.find((rect) =>
      pointInsideRect(next, expandRect(rect, clearance, clearance)),
    );
    if (!obstacle) break;
    const rect = expandRect(obstacle, clearance, clearance);
    const candidates = [
      { distance: Math.abs(next.x - rect.left), point: { x: rect.left, y: next.y } },
      { distance: Math.abs(rect.right - next.x), point: { x: rect.right, y: next.y } },
      { distance: Math.abs(next.y - rect.top), point: { x: next.x, y: rect.top } },
      { distance: Math.abs(rect.bottom - next.y), point: { x: next.x, y: rect.bottom } },
    ];
    candidates.sort((a, b) => a.distance - b.distance);
    next = candidates[0].point;
  }
  return next;
}

export function segmentRectInterval(start: Point, end: Point, rect: Rect): [number, number] | null {
  let minimum = 0;
  let maximum = 1;
  for (const [origin, delta, lower, upper] of [
    [start.x, end.x - start.x, rect.left, rect.right],
    [start.y, end.y - start.y, rect.top, rect.bottom],
  ] as const) {
    if (Math.abs(delta) < 0.0001) {
      if (origin < lower || origin > upper) return null;
      continue;
    }
    const first = (lower - origin) / delta;
    const second = (upper - origin) / delta;
    minimum = Math.max(minimum, Math.min(first, second));
    maximum = Math.min(maximum, Math.max(first, second));
    if (minimum > maximum) return null;
  }
  return [minimum, maximum];
}

export function nearestClearRatio(
  start: Point,
  end: Point,
  preferred: number,
  obstacles: Rect[],
  horizontalClearance: number,
  verticalClearance: number,
): number {
  const expanded = obstacles.map((rect) =>
    expandRect(rect, horizontalClearance, verticalClearance),
  );
  const isClear = (ratio: number) =>
    !expanded.some((rect) => pointInsideRect(pointOnLine(start, end, ratio), rect));
  if (isClear(preferred)) return preferred;
  const candidates = [0, 1];
  for (const rect of expanded) {
    const interval = segmentRectInterval(start, end, rect);
    if (!interval) continue;
    candidates.push(
      Math.max(0, interval[0] - 0.002),
      Math.min(1, interval[1] + 0.002),
    );
  }
  candidates.sort((a, b) => Math.abs(a - preferred) - Math.abs(b - preferred));
  return candidates.find(isClear) ?? preferred;
}

export function averagePoint(points: Point[]): Point {
  const total = points.reduce(
    (sum, point) => ({ x: sum.x + point.x, y: sum.y + point.y }),
    { x: 0, y: 0 },
  );
  return { x: total.x / points.length, y: total.y / points.length };
}

export function pointOnLine(start: Point, end: Point, ratio: number): Point {
  return {
    x: start.x + (end.x - start.x) * ratio,
    y: start.y + (end.y - start.y) * ratio,
  };
}

export function cardCenter(
  card: Col,
  centerX: (c: Col) => number,
  rowTop: (row: number) => number,
): Point {
  return { x: centerX(card), y: rowTop(card.row) + ROW_H / 2 };
}

export function rectBoundary(center: Point, halfWidth: number, halfHeight: number, toward: Point): Point {
  const dx = toward.x - center.x;
  const dy = toward.y - center.y;
  if (dx === 0 && dy === 0) return center;
  const scale = 1 / Math.max(Math.abs(dx) / halfWidth, Math.abs(dy) / halfHeight);
  return { x: center.x + dx * scale, y: center.y + dy * scale };
}

export function cardBoundary(
  card: Col,
  toward: Point,
  centerX: (c: Col) => number,
  rowTop: (row: number) => number,
): Point {
  return rectBoundary(
    cardCenter(card, centerX, rowTop),
    card.width / 2,
    card.height / 2,
    toward,
  );
}

export function edgePlacementKey(from: string, to: string) {
  return `${from}->${to}`;
}

export function gateWireKey(targetId: string) {
  return `gate:${targetId}`;
}

export function svgPointerPoint<T extends SVGGraphicsElement>(
  event: React.PointerEvent<T>,
): Point | null {
  const svg = event.currentTarget.ownerSVGElement;
  const matrix = svg?.getScreenCTM();
  if (!svg || !matrix) return null;
  const point = svg.createSVGPoint();
  point.x = event.clientX;
  point.y = event.clientY;
  const local = point.matrixTransform(matrix.inverse());
  return { x: local.x, y: local.y };
}

export function closestPointOnPolyline(points: Point[], pointer: Point) {
  let closest = points[0];
  let segmentIndex = 0;
  let distanceSquared = Number.POSITIVE_INFINITY;
  for (let index = 0; index < points.length - 1; index++) {
    const start = points[index];
    const end = points[index + 1];
    const dx = end.x - start.x;
    const dy = end.y - start.y;
    const lengthSquared = dx * dx + dy * dy;
    const ratio =
      lengthSquared === 0
        ? 0
        : Math.max(
            0,
            Math.min(
              1,
              ((pointer.x - start.x) * dx + (pointer.y - start.y) * dy) /
                lengthSquared,
            ),
          );
    const projected = { x: start.x + dx * ratio, y: start.y + dy * ratio };
    const px = pointer.x - projected.x;
    const py = pointer.y - projected.y;
    const candidateDistance = px * px + py * py;
    if (candidateDistance < distanceSquared) {
      closest = projected;
      segmentIndex = index;
      distanceSquared = candidateDistance;
    }
  }
  return { point: closest, segmentIndex };
}
