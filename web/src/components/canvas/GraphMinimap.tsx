/**
 * The canvas minimap.
 *
 * A whole-board overview with the current viewport drawn on it; clicking
 * moves the view there. Positions are percentages of the board, so it stays
 * correct at any zoom without recomputing the layout.
 */

import { ROW_H, type Col } from "./geometry";

export function GraphMinimap({
  cols,
  selectedGraphNodeIDs,
  boardRef,
  boardW,
  svgH,
  zoom,
  boardScroll,
  boardViewport,
  centerX,
  rowTop,
}: {
  cols: Col[];
  selectedGraphNodeIDs: Set<string>;
  boardRef: React.RefObject<HTMLDivElement | null>;
  boardW: number;
  svgH: number;
  zoom: number;
  boardScroll: { left: number; top: number };
  boardViewport: { width: number; height: number };
  centerX: (column: Col) => number;
  rowTop: (row: number) => number;
}) {
  return (
    <div
      className="graph-minimap"
      role="img"
      aria-label="關係圖縮圖；點擊可移動視角"
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
      {cols.map((column) => (
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
      ))}
      <span
        className="graph-minimap-viewport"
        style={{
          left: `${(boardScroll.left / zoom / boardW) * 100}%`,
          top: `${(boardScroll.top / zoom / svgH) * 100}%`,
          width: `${Math.min(100, (boardViewport.width / zoom / boardW) * 100)}%`,
          height: `${Math.min(100, (boardViewport.height / zoom / svgH) * 100)}%`,
        }}
      />
    </div>
  );
}
