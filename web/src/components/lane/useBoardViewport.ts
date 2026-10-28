/**
 * What the board can currently see: the size of the scrolling element. Both
 * panes measure themselves the same way and both get an opening scroll: the
 * graph frames its content — or, when this session has shown the project
 * before, comes back to the scroll it was left at (boardViewMemory) — and the
 * timeline opens on today (timelineOpeningScroll). How far it has scrolled is read by whatever
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
  openingCorner,
  timelineOpening,
  timelineOrientation,
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
   * What the opening frames — the cards, the groups and annotations, and the
   * empty-state hint over a board that has no cards — in board pixels before
   * the zoom. A ref to a getter because the
   * board's origin depends on the viewport measured here, and because only
   * the opening needs them: it calls it after the render that set it.
   */
  openingCards?: RefObject<() => Rect[]>;
  /**
   * What floats over the view's bottom-right corner — the minimap — in view
   * pixels, read with the cards: the opening keeps them out from under it.
   */
  openingCorner?: RefObject<() => { width: number; height: number } | null>;
  /** The timeline's opening scroll, read the same way as openingCards. */
  timelineOpening?: RefObject<() => { left: number; top: number }>;
  /** A new orientation is a new layout, so it opens afresh too. */
  timelineOrientation?: "vertical" | "horizontal";
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
  // its content (openingScroll): centred when they fit, so a fresh project's
  // single node sits in the middle with room on every side, else from their
  // top-left edge; a board without content opens at the buffer origin, where
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
      : openingScroll(
          openingCards?.current?.() ?? [],
          zoom,
          boardViewport,
          openingCorner?.current?.(),
        );
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

  // The timeline's range starts two weeks back, so left at 0 it opened on a
  // fortnight of empty days. It opens where timelineOpening says — once per
  // project and orientation, and again whenever the pane is reopened, since a
  // collapsed pane drops its scrolling element.
  const timelineViewportKeyRef = useRef("");
  useLayoutEffect(() => {
    if (isGraph) return;
    if (collapsed) {
      timelineViewportKeyRef.current = "";
      return;
    }
    const board = boardRef.current;
    if (!board || boardViewport.width <= 0 || boardViewport.height <= 0) return;
    if (!contentReady || !timelineOpening?.current) return;
    const viewportKey = `${activeProject ?? ""}
${timelineOrientation ?? ""}`;
    if (timelineViewportKeyRef.current === viewportKey) return;
    const scroll = timelineOpening.current();
    board.scrollLeft = scroll.left;
    board.scrollTop = scroll.top;
    timelineViewportKeyRef.current = viewportKey;
  }, [
    activeProject,
    boardViewport.height,
    boardViewport.width,
    collapsed,
    contentReady,
    isGraph,
    timelineOrientation,
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
