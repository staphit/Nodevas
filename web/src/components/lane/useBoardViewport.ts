/**
 * What the board can currently see: the size of the scrolling element. Both
 * panes measure themselves the same way; only the graph gets an opening scroll
 * position — or, when this session has shown the project before, the scroll it
 * was left at (boardViewMemory). How far it has scrolled is read by whatever
 * follows it (useViewWindow, the minimap), so a pan does not render the board.
 */

import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import {
  boardViewKey,
  recallBoardView,
  rememberBoardView,
  restoredScroll,
} from "../canvas/boardViewMemory";
import type { Rect } from "../canvas/geometry";
import { openingScroll } from "./boardGeometry";

export function useBoardViewport({
  boardRef,
  collapsed,
  isGraph,
  activeProject,
  zoom,
  userZoom = zoom,
  openingCards,
  contentReady = true,
}: {
  boardRef: RefObject<HTMLDivElement | null>;
  collapsed?: boolean;
  isGraph: boolean;
  activeProject: string | null;
  /** The scale the board is drawn at. */
  zoom: number;
  /** The user's own zoom, which is what a later mount starts from. */
  userZoom?: number;
  /**
   * The cards, in board pixels before the zoom. A ref to a getter because the
   * board's origin depends on the viewport measured here, and because only
   * the opening needs them: it calls it after the render that set it.
   */
  openingCards?: RefObject<() => Rect[]>;
  /**
   * False while the board still holds another project's graph, or none yet:
   * the opening scroll frames cards, so it waits for the right ones.
   */
  contentReady?: boolean;
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

  // The graph lives inside a larger virtual surface. A project opens framing
  // its cards (openingScroll): centred when they fit, so a fresh project's
  // single node sits in the middle with room on every side, else from their
  // top-left edge; a board without cards opens at the buffer origin, where
  // logical (0,0) is. A project this session has already shown comes back
  // where it was left.
  useLayoutEffect(() => {
    if (!isGraph) return;
    if (collapsed) {
      graphViewportKeyRef.current = "";
      return;
    }
    const board = boardRef.current;
    if (!board || boardViewport.width <= 0 || boardViewport.height <= 0) return;
    const viewportKey = boardViewKey(activeProject);
    if (graphViewportKeyRef.current === viewportKey) return;
    const remembered = recallBoardView(viewportKey);
    if (!remembered && !contentReady) return;
    const scroll = remembered
      ? restoredScroll(remembered, zoom, boardViewport)
      : openingScroll(openingCards?.current?.() ?? [], zoom, boardViewport);
    board.scrollLeft = scroll.left;
    board.scrollTop = scroll.top;
    graphViewportKeyRef.current = viewportKey;
  }, [
    activeProject,
    boardViewport.height,
    boardViewport.width,
    collapsed,
    contentReady,
    isGraph,
    zoom,
  ]);

  // Written as the board scrolls and as the zoom changes, so whatever unmounts
  // the board finds it already recorded. Only once the opening scroll above has
  // landed for this project: before that the scroll is the browser's 0,0, and
  // recording it would overwrite the view the opening is about to restore.
  const scaleRef = useRef({ zoom, userZoom });
  scaleRef.current = { zoom, userZoom };
  const record = () => {
    const board = boardRef.current;
    const key = boardViewKey(activeProject);
    if (!board || !isGraph || collapsed || graphViewportKeyRef.current !== key) return;
    rememberBoardView(key, {
      zoom: scaleRef.current.userZoom,
      scale: scaleRef.current.zoom,
      left: board.scrollLeft,
      top: board.scrollTop,
    });
  };
  const recordRef = useRef(record);
  recordRef.current = record;
  useEffect(() => {
    const board = boardRef.current;
    if (!board || !isGraph || collapsed) return;
    const onScroll = () => recordRef.current();
    board.addEventListener("scroll", onScroll, { passive: true });
    return () => board.removeEventListener("scroll", onScroll);
  }, [boardRef, collapsed, isGraph]);
  useEffect(() => {
    recordRef.current();
  }, [zoom, userZoom, activeProject, boardViewport.width, boardViewport.height]);

  return { boardViewport };
}
