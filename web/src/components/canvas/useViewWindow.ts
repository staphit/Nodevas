/**
 * The part of the board a culled canvas draws: the visible area plus a margin,
 * in board units. Read once before paint, then again at most once a frame
 * while the board scrolls, and whenever the zoom or the board's size changes.
 * The state only changes when the snapped window does, so most frames of a pan
 * render nothing at all.
 *
 * A pan frame reads nothing but the scroll position, and reads it in the
 * scroll event, before any frame callback has written to the page: the size
 * is the measured one handed in (useBoardViewport's ResizeObserver). Read in
 * the frame, after the minimap had moved its frame, or reading clientWidth,
 * it was a forced layout of every mounted card whenever the cards changed.
 */

import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { sameViewWindow, viewWindowFor, type ViewWindow } from "./viewportCulling";

export function useViewWindow({
  boardRef,
  enabled,
  zoom,
  viewport,
}: {
  boardRef: RefObject<HTMLDivElement | null>;
  /** Whether this board culls at all; when it does not the window is null. */
  enabled: boolean;
  zoom: number;
  /** The board's measured size, so a resize re-reads the window. */
  viewport: { width: number; height: number };
}): ViewWindow {
  const [view, setView] = useState<ViewWindow>("measuring");
  // Compared here, not in a setView updater: an updater that hands back the
  // same window still renders the board once whenever React cannot bail out
  // early, as right after the window last moved.
  const viewRef = useRef<ViewWindow>("measuring");
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;

  useLayoutEffect(() => {
    if (!enabled) return;
    const board = boardRef.current;
    if (!board) return;
    // Measured once per run of this effect, never per frame. Before the
    // viewport has been measured the board is asked directly, so the first
    // render is culled rather than drawing every card.
    const size =
      viewport.width > 0 && viewport.height > 0
        ? { width: viewport.width, height: viewport.height }
        : { width: board.clientWidth, height: board.clientHeight };
    let scroll = { left: board.scrollLeft, top: board.scrollTop };
    const apply = () => {
      const next = viewWindowFor(scroll, size, zoomRef.current);
      if (sameViewWindow(viewRef.current, next)) return;
      viewRef.current = next;
      setView(next);
    };
    apply();
    let frame = 0;
    const onScroll = () => {
      scroll = { left: board.scrollLeft, top: board.scrollTop };
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        apply();
      });
    };
    board.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      board.removeEventListener("scroll", onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [boardRef, enabled, zoom, viewport.width, viewport.height]);

  return enabled ? view : null;
}
