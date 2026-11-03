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

/** A pan is recorded once its scroll events stop for this long. */
const PAN_SETTLE_MS = 150;

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

  // Recorded once a pan settles and as the zoom changes, so whatever unmounts
  // the board finds it already recorded. Only once the opening scroll above has
  // landed for this project: before that the scroll is the browser's 0,0, and
  // recording it would overwrite the view the opening is about to restore.
  //
  // A scroll event only notes that the view moved and what it was showing:
  // reading the scroll position there made every pan frame ask for an
  // up-to-date layout, a forced layout of every mounted card whenever the
  // frame before had written anything. The position is read when the
  // scrolling settles, or before the view is left (a project switch, an
  // unmount), whichever comes first.
  const scaleRef = useRef({ zoom, userZoom });
  scaleRef.current = { zoom, userZoom };
  const write = (
    board: HTMLDivElement,
    key: string,
    scale: { zoom: number; userZoom: number },
  ) => {
    rememberBoardView(key, {
      zoom: scale.userZoom,
      scale: scale.zoom,
      left: board.scrollLeft,
      top: board.scrollTop,
    });
  };
  const record = () => {
    const board = boardRef.current;
    const key = boardViewKey(activeProject);
    if (!board || !isGraph || collapsed || graphViewportKeyRef.current !== key) return;
    write(board, key, scaleRef.current);
  };
  const recordRef = useRef(record);
  recordRef.current = record;
  /** A pan not yet recorded: the board, the view it belongs to and its scale. */
  const pendingRef = useRef<{
    board: HTMLDivElement;
    key: string;
    scale: { zoom: number; userZoom: number };
  } | null>(null);
  const flushPending = () => {
    const pending = pendingRef.current;
    pendingRef.current = null;
    // A board already taken out of the page has no scroll left to read.
    if (pending && pending.board.isConnected) write(pending.board, pending.key, pending.scale);
  };
  const flushRef = useRef(flushPending);
  flushRef.current = flushPending;
  useEffect(() => {
    const board = boardRef.current;
    if (!board || !isGraph || collapsed) return;
    let settle: ReturnType<typeof setTimeout> | undefined;
    const onScroll = () => {
      const key = graphViewportKeyRef.current;
      if (key === "") return;
      pendingRef.current = { board, key, scale: scaleRef.current };
      clearTimeout(settle);
      settle = setTimeout(() => flushRef.current(), PAN_SETTLE_MS);
    };
    board.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      board.removeEventListener("scroll", onScroll);
      clearTimeout(settle);
    };
  }, [boardRef, collapsed, isGraph]);
  // Before the view changes hands — another project, a collapse, the board
  // unmounting — the pan it was left at is read while the board still shows
  // it: a commit runs its layout cleanups before any of its layout effects,
  // so this comes before the opening above scrolls to the next view.
  useLayoutEffect(() => () => flushRef.current(), [activeProject, collapsed, isGraph]);
  useEffect(() => {
    pendingRef.current = null;
    recordRef.current();
  }, [zoom, userZoom, activeProject, boardViewport.width, boardViewport.height]);

  return { boardViewport };
}
