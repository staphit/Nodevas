/**
 * The part of the board a culled canvas draws: the visible area plus a margin,
 * in board units. Read once before paint, then again at most once a frame
 * while the board scrolls, and whenever the zoom or the board's size changes.
 * The state only changes when the snapped window does, so most frames of a pan
 * render nothing at all.
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
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;

  useLayoutEffect(() => {
    if (!enabled) return;
    const board = boardRef.current;
    if (!board) return;
    const read = () => {
      const next = viewWindowFor(
        { left: board.scrollLeft, top: board.scrollTop },
        { width: board.clientWidth, height: board.clientHeight },
        zoomRef.current,
      );
      setView((current) => (sameViewWindow(current, next) ? current : next));
    };
    read();
    let frame = 0;
    const onScroll = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        read();
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
