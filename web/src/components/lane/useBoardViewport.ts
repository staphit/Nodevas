/**
 * What the board can currently see: the size of the scrolling element. Both
 * panes measure themselves the same way; only the graph gets an opening scroll
 * position. How far it has scrolled is read by whatever follows it
 * (useViewWindow, the minimap), so a pan does not render the board.
 */

import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { GRAPH_PAN_BUFFER_X, GRAPH_PAN_BUFFER_Y } from "./boardGeometry";

export function useBoardViewport({
  boardRef,
  collapsed,
  isGraph,
  activeProject,
  zoom,
}: {
  boardRef: RefObject<HTMLDivElement | null>;
  collapsed?: boolean;
  isGraph: boolean;
  activeProject: string | null;
  zoom: number;
}) {
  const [boardViewport, setBoardViewport] = useState({ width: 0, height: 0 });
  const graphViewportKeyRef = useRef("");

  useLayoutEffect(() => {
    const board = boardRef.current;
    if (!board) return;
    const updateViewport = () => {
      const width = board.clientWidth;
      const height = board.clientHeight;
      setBoardViewport((current) =>
        current.width === width && current.height === height ? current : { width, height },
      );
    };
    updateViewport();
    const observer = new ResizeObserver(updateViewport);
    observer.observe(board);
    return () => observer.disconnect();
  }, [collapsed]);

  // The graph lives inside a larger virtual surface. A project that fits the
  // viewport opens centred in it, so a fresh project's single node sits in the
  // middle with room on every side instead of pinned to the top-left corner;
  // anything larger opens at the buffer origin, where logical (0,0) is.
  useLayoutEffect(() => {
    if (!isGraph) return;
    if (collapsed) {
      graphViewportKeyRef.current = "";
      return;
    }
    const board = boardRef.current;
    if (!board || boardViewport.width <= 0 || boardViewport.height <= 0) return;
    const viewportKey = activeProject || "__default__";
    if (graphViewportKeyRef.current === viewportKey) return;
    board.scrollLeft = GRAPH_PAN_BUFFER_X * zoom;
    board.scrollTop = GRAPH_PAN_BUFFER_Y * zoom;
    graphViewportKeyRef.current = viewportKey;
  }, [
    activeProject,
    boardViewport.height,
    boardViewport.width,
    collapsed,
    isGraph,
    zoom,
  ]);

  return { boardViewport };
}
