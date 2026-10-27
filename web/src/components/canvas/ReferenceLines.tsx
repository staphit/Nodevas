/**
 * Reference wires [references].
 *
 * A node's `refs` name the Notes it is written against — a character sheet a
 * scene leans on. They never block, so they must never read as a dependency:
 * a thin dotted curve with no arrowhead, drawn under every dependency wire and
 * never hit-tested. The curve bends off the straight line so it does not sit
 * on top of a dependency wire between the same two cards.
 *
 * Hovering a card lights up its references. The hovered card is kept in a
 * tiny store of its own so a hover redraws these wires and nothing else on the
 * board.
 */

import { useSyncExternalStore } from "react";

import { useI18n } from "../../i18n";
import type { Graph } from "../../types";
import { cardBoundary, cardCenter, type Col, type Point } from "./geometry";

/** Past this many pairs only the ones touching an active card are drawn. */
export const MAX_REFERENCE_PAIRS = 300;

let hoveredCard: string | null = null;
const listeners = new Set<() => void>();

/** Called by the cards on pointer enter / leave. */
export function setHoveredReferenceCard(id: string | null): void {
  if (hoveredCard === id) return;
  hoveredCard = id;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const readHovered = () => hoveredCard;

export interface ReferencePair {
  /** The note being used. */
  from: string;
  /** The node using it. */
  to: string;
}

/** Every reference whose two ends are on the board, in graph order. */
export function referencePairs(graph: Graph | null, onBoard: (id: string) => boolean): ReferencePair[] {
  const pairs: ReferencePair[] = [];
  for (const node of graph?.nodes ?? []) {
    if (!node.refs?.length || !onBoard(node.id)) continue;
    for (const ref of node.refs) {
      if (ref !== node.id && onBoard(ref)) pairs.push({ from: ref, to: node.id });
    }
  }
  return pairs;
}

function curve(start: Point, end: Point): string {
  const mx = (start.x + end.x) / 2;
  const my = (start.y + end.y) / 2;
  const dx = end.x - start.x;
  const dy = end.y - start.y;
  // A gentle bow to one side, proportional to the length.
  const bend = 0.18;
  const cx = mx - dy * bend;
  const cy = my + dx * bend;
  return `M ${start.x} ${start.y} Q ${cx} ${cy} ${end.x} ${end.y}`;
}

export function ReferenceLines({
  graph,
  colOf,
  centerX,
  rowTop,
  activeIds,
}: {
  graph: Graph | null;
  colOf: Map<string, Col>;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
  /** Selected cards; the hovered card joins them. */
  activeIds: ReadonlySet<string>;
}) {
  const { t } = useI18n();
  const hovered = useSyncExternalStore(subscribe, readHovered, readHovered);
  const active = (id: string) => activeIds.has(id) || id === hovered;
  const all = referencePairs(graph, (id) => colOf.has(id));
  if (all.length === 0) return null;
  const pairs =
    all.length > MAX_REFERENCE_PAIRS
      ? all.filter((pair) => active(pair.from) || active(pair.to))
      : all;
  return (
    <g className="reference-lines">
      <title>{t("canvas.reference.title")}</title>
      {pairs.map(({ from, to }) => {
        const note = colOf.get(from)!;
        const user = colOf.get(to)!;
        const noteCenter = cardCenter(note, centerX, rowTop);
        const userCenter = cardCenter(user, centerX, rowTop);
        const start = cardBoundary(note, userCenter, centerX, rowTop);
        const end = cardBoundary(user, noteCenter, centerX, rowTop);
        const lit = active(from) || active(to);
        return (
          <g key={`${from}->${to}`} data-reference={`${from}->${to}`}>
            <path className={`reference-wire${lit ? " active" : ""}`} d={curve(start, end)} />
            <circle className={`reference-dot${lit ? " active" : ""}`} cx={end.x} cy={end.y} r={2} />
          </g>
        );
      })}
    </g>
  );
}
