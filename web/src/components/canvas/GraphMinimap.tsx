/**
 * The canvas minimap.
 *
 * A whole-board overview with the current viewport drawn on it; clicking
 * moves the view there. Positions are percentages of the board, so it stays
 * correct at any zoom without recomputing the layout.
 */

import { useLayoutEffect, useMemo, useRef } from "react";
import { ROW_H, type Col } from "./geometry";
import { useI18n } from "../../i18n";

export function GraphMinimap({
  cols,
  selectedGraphNodeIDs,
  boardRef,
  boardW,
  svgH,
  zoom,
  boardViewport,
  graphOffsetX,
  graphOffsetY,
  centerX,
  rowTop,
}: {
  cols: Col[];
  selectedGraphNodeIDs: Set<string>;
  boardRef: React.RefObject<HTMLDivElement | null>;
  boardW: number;
  svgH: number;
  zoom: number;
  boardViewport: { width: number; height: number };
  /** Where the graph sits on the board: with the cards, all centerX and rowTop read. */
  graphOffsetX: number;
  graphOffsetY: number;
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
}) {
  const { t } = useI18n();
  // The board hands down new layout functions every render; the dots are laid
  // out again only when what those functions read changes. On a large board
  // that is a few thousand elements a pan no longer touches.
  const dots = useMemo(
    () =>
      cols.map((column) => (
        <span
          key={column.node.id}
          className={`graph-minimap-node${
            selectedGraphNodeIDs.has(column.node.id) ? " selected" : ""
          }`}
          style={{
            left: `${((centerX(column) - column.width / 2) / boardW) * 100}%`,
            top: `${((rowTop(column.row) + ROW_H / 2 - column.height / 2) / svgH) * 100}%`,
            width: `${Math.max(1.5, (column.width / boardW) * 100)}%`,
            height: `${Math.max(2, (column.height / svgH) * 100)}%`,
          }}
        />
      )),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- centerX and
    // rowTop are new every render and read only the offsets listed here.
    [cols, selectedGraphNodeIDs, boardW, svgH, graphOffsetX, graphOffsetY],
  );
  // The frame follows the board's scroll by itself, one style write a frame,
  // so a pan renders neither the board nor the map.
  const frameRef = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const board = boardRef.current;
    const frame = frameRef.current;
    if (!board || !frame) return;
    const place = () => {
      frame.style.left = `${(board.scrollLeft / zoom / boardW) * 100}%`;
      frame.style.top = `${(board.scrollTop / zoom / svgH) * 100}%`;
    };
    place();
    let pending = 0;
    const onScroll = () => {
      if (pending) return;
      pending = requestAnimationFrame(() => {
        pending = 0;
        place();
      });
    };
    board.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      board.removeEventListener("scroll", onScroll);
      if (pending) cancelAnimationFrame(pending);
    };
  }, [boardRef, zoom, boardW, svgH]);
  return (
    <div
      className="graph-minimap"
      role="img"
      aria-label={t("canvas.minimapAria")}
      onPointerDown={(event) => {
        const board = boardRef.current;
        if (!board) return;
        const rect = event.currentTarget.getBoundingClientRect();
        const contentX = ((event.clientX - rect.left) / rect.width) * boardW;
        const contentY = ((event.clientY - rect.top) / rect.height) * svgH;
        board.scrollLeft = contentX * zoom - board.clientWidth / 2;
        board.scrollTop = contentY * zoom - board.clientHeight / 2;
      }}
    >
      {dots}
      <span
        ref={frameRef}
        className="graph-minimap-viewport"
        style={{
          width: `${Math.min(100, (boardViewport.width / zoom / boardW) * 100)}%`,
          height: `${Math.min(100, (boardViewport.height / zoom / svgH) * 100)}%`,
        }}
      />
    </div>
  );
}
