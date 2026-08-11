import { useRef, useState, type PointerEvent } from "react";
import { COL_W, ROW_H, snapDragDelta, type Col, type Rect } from "./geometry";
import {
  NO_SNAP_TARGETS,
  SNAP_THRESHOLD_PX,
  buildSnapTargets,
  rectOfColumn,
  solveSnap,
  unionRect,
  type SnapBox,
  type SnapGuide,
  type SnapTargets,
} from "./snapping";

const MAX_COL = 499;
const MAX_ROW = 200;

export interface CardDragState {
  id: string;
  ids: string[];
  dx: number;
  dy: number;
  moved: boolean;
  /** Alignment lines the cards are currently sitting on. */
  guides: SnapGuide[];
}

export function useCardDrag({
  zoom,
  columns,
  selectedIds,
  onSelect,
  onOpen,
  onMove,
  onReject,
  snapToGrid = false,
  canMove = true,
  snapEnabled = false,
  snapBoxes = [],
}: {
  zoom: number;
  columns: Col[];
  selectedIds: ReadonlySet<string>;
  onSelect: (nodeId: string) => void;
  onOpen: (nodeId: string) => void;
  onMove: (positions: Record<string, { x: number; y: number }>, count: number) => void;
  onReject: (message: string) => void;
  /**
   * Whether a dropped card lands on the grid instead of wherever the pointer
   * left it. Holding Alt suspends it for the rest of the gesture, which is how
   * every diagram editor spells "not this one".
   */
  snapToGrid?: boolean;
  /**
   * Whether the cards align themselves to their neighbours while they move.
   * Holding Shift suspends it for one gesture — Alt is already the connection
   * drag and Ctrl the marquee, so Shift is the modifier left.
   */
  snapEnabled?: boolean;
  /** Everything on the board that can be aligned to, cards and decorations. */
  snapBoxes?: readonly SnapBox[];
  /**
   * Whether a card may be dragged to a new cell. False for a read-only
   * session, where the gesture still selects and still opens on a click, but
   * the card never leaves its place.
   *
   * The move is stopped here rather than at the commit: a card that follows
   * the pointer and then springs back reads as a failed save, and this one was
   * never going to be attempted.
   */
  canMove?: boolean;
}) {
  const dragStart = useRef({ x: 0, y: 0 });
  // The box the moving cards occupy at rest. Snapping always works from this
  // plus the raw pointer delta, so a correction never feeds into the next move.
  const dragBounds = useRef<Rect | null>(null);
  const dragTargets = useRef<SnapTargets>(NO_SNAP_TARGETS);
  const dragRef = useRef<CardDragState | null>(null);
  const [state, setState] = useState<CardDragState | null>(null);

  const onPointerDown = (event: PointerEvent<HTMLElement>, id: string) => {
    if (event.button !== 0) return;
    const ids = selectedIds.has(id) ? Array.from(selectedIds) : [id];
    if (!selectedIds.has(id)) onSelect(id);
    event.currentTarget.setPointerCapture(event.pointerId);
    dragStart.current = { x: event.clientX, y: event.clientY };
    const moving = new Set(ids);
    dragBounds.current = unionRect(
      columns.filter((column) => moving.has(column.node.id)).map(rectOfColumn),
    );
    dragTargets.current = snapEnabled ? buildSnapTargets(snapBoxes, moving) : NO_SNAP_TARGETS;
    const next = { id, ids, dx: 0, dy: 0, moved: false, guides: [] };
    dragRef.current = next;
    setState(next);
  };

  const onPointerMove = (event: PointerEvent<HTMLElement>) => {
    const current = dragRef.current;
    if (!current) return false;
    // The gesture stays live so the pointer-up below can still open the card,
    // but nothing about its position changes.
    if (!canMove) return true;
    const screenDx = event.clientX - dragStart.current.x;
    const screenDy = event.clientY - dragStart.current.y;
    // Both snaps are resolved here rather than at the drop so the card previews
    // where it will actually land. Snapping only at commit makes the card jump
    // out from under the pointer on release, which reads as the board
    // rejecting the move.
    const bounds = dragBounds.current;
    // A whole selection moves as one block, so the bounding box is what aligns,
    // not each card in it.
    const aligned =
      snapEnabled && !event.shiftKey && bounds
        ? solveSnap(
            bounds,
            screenDx / zoom,
            screenDy / zoom,
            dragTargets.current,
            SNAP_THRESHOLD_PX / zoom,
          )
        : { dx: screenDx / zoom, dy: screenDy / zoom, guides: [] };
    // Fine before coarse: the neighbour lines are consulted first, then the
    // lattice rounds whatever they agreed on, because a board with the grid
    // switched on has cells a card has to sit in and a spot between them is not
    // one of them. An axis the lattice pulled off its line loses that guide —
    // a line drawn where the card is not is worse than no line at all.
    let dx = aligned.dx;
    let dy = aligned.dy;
    let guides = aligned.guides;
    const anchor = columns.find((column) => column.node.id === current.id);
    if (snapToGrid && !event.altKey && anchor) {
      dx = snapDragDelta(anchor.index, dx / COL_W) * COL_W;
      dy = snapDragDelta(anchor.row, dy / ROW_H) * ROW_H;
      guides = guides.filter((guide) =>
        guide.axis === "x" ? dx === aligned.dx : dy === aligned.dy,
      );
    }
    const next = {
      ...current,
      dx,
      dy,
      guides,
      // Click-versus-drag stays on the raw pointer distance: with snapping on,
      // the first half cell of travel resolves to no movement at all, and a
      // gesture that far is no longer a click.
      moved: current.moved || Math.abs(screenDx) + Math.abs(screenDy) > 5,
    };
    dragRef.current = next;
    setState(next);
    return true;
  };

  const onPointerUp = (event: PointerEvent<HTMLElement>, id: string) => {
    const current = dragRef.current;
    if (!current || current.id !== id) return false;
    const wasMoved = current.moved;
    const { dx, dy } = current;
    dragRef.current = null;
    dragBounds.current = null;
    dragTargets.current = NO_SNAP_TARGETS;
    setState(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (!wasMoved) {
      onOpen(id);
      return true;
    }

    const movingIds = new Set(current.ids);
    const movingColumns = columns.filter((column) => movingIds.has(column.node.id));
    if (movingColumns.length === 0) return true;
    const minCol = Math.min(...movingColumns.map((column) => column.index));
    const maxCol = Math.max(...movingColumns.map((column) => column.index));
    const minRow = Math.min(...movingColumns.map((column) => column.row));
    const maxRow = Math.max(...movingColumns.map((column) => column.row));
    // Dragging past the top-left corner is allowed: the board slides instead
    // of the card stopping dead, so `onMove` may receive negative cells.
    const deltaCol = Math.max(-(MAX_COL + minCol), Math.min(MAX_COL - maxCol, dx / COL_W));
    const deltaRow = Math.max(-(MAX_ROW + minRow), Math.min(MAX_ROW - maxRow, dy / ROW_H));
    const nextPositions = new Map(
      movingColumns.map((column) => [
        column.node.id,
        {
          // Three decimals, not two: a snapped edge has to survive the trip
          // through cells, and 0.01 of a cell is a visible pixel when zoomed in.
          x: Math.round((column.index + deltaCol) * 1000) / 1000,
          y: Math.round((column.row + deltaRow) * 1000) / 1000,
        },
      ]),
    );
    const changed = movingColumns.some((column) => {
      const next = nextPositions.get(column.node.id);
      return next && (next.x !== column.index || next.y !== column.row);
    });
    if (!changed) return true;

    const stationaryColumns = columns.filter((column) => !movingIds.has(column.node.id));
    const overlap = movingColumns
      .map((column) => {
        const next = nextPositions.get(column.node.id);
        if (!next) return null;
        const stationary = stationaryColumns.find(
          (candidate) =>
            Math.abs((next.x - candidate.index) * COL_W) <
              (column.width + candidate.width) / 2 + 8 &&
            Math.abs((next.y - candidate.row) * ROW_H) <
              (column.height + candidate.height) / 2 + 8,
        );
        return stationary ? { stationary } : null;
      })
      .find((result) => result !== null);
    if (overlap) {
      onReject("節點不可重疊：" + (overlap.stationary.node.title || overlap.stationary.node.id));
      return true;
    }

    onMove(
      {
        ...Object.fromEntries(columns.map((column) => [column.node.id, { x: column.index, y: column.row }])),
        ...Object.fromEntries(nextPositions),
      },
      movingColumns.length,
    );
    return true;
  };

  const cancel = () => {
    if (!dragRef.current) return false;
    dragRef.current = null;
    dragBounds.current = null;
    dragTargets.current = NO_SNAP_TARGETS;
    setState(null);
    return true;
  };

  return {
    state,
    handlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp,
      onPointerCancel: cancel,
      cancel,
    },
  };
}
