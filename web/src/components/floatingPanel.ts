/**
 * Keeping a floating panel on screen, and letting one be moved by hand.
 *
 * Every menu in the app used to reserve a hardcoded width and height and clamp
 * its corner against that guess. A guess overhangs whenever it is wrong, and it
 * is wrong for any panel whose size depends on what is inside it — the node
 * form grows with each custom status. Measuring costs one layout pass, before
 * paint, and is right for all of them.
 */

import { useLayoutEffect, useRef, useState } from "react";

/** Gap kept between a panel and the window edge. */
export const VIEWPORT_MARGIN = 8;

export interface PanelPoint {
  left: number;
  top: number;
}

export function clampToViewport(
  point: PanelPoint,
  width: number,
  height: number,
  margin = VIEWPORT_MARGIN,
): PanelPoint {
  // A panel bigger than the window has no placement that fits, and then the
  // near edge is the one to keep: that is where its heading and close button
  // are, and it scrolls inside its own max-height for the rest.
  return {
    left: Math.max(margin, Math.min(point.left, window.innerWidth - width - margin)),
    top: Math.max(margin, Math.min(point.top, window.innerHeight - height - margin)),
  };
}

/**
 * Places a panel at `anchor`, keeps it on screen, and — through the returned
 * handlers — lets the user drag it somewhere else.
 *
 * Screen pixels throughout, not board units: the panel is fixed to the window,
 * and where it sits has nothing to do with where the thing it acts on lives.
 */
export function useFloatingPanel<T extends HTMLElement>(anchor: PanelPoint) {
  const ref = useRef<T>(null);
  // Null until the user moves it: an untouched panel follows its anchor, a
  // moved one stays where it was put.
  const [moved, setMoved] = useState<PanelPoint | null>(null);
  const [placement, setPlacement] = useState<PanelPoint>(anchor);
  const [dragging, setDragging] = useState(false);

  useLayoutEffect(() => {
    const place = () => {
      const panel = ref.current;
      if (!panel) return;
      setPlacement(
        clampToViewport(moved ?? anchor, panel.offsetWidth, panel.offsetHeight),
      );
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [anchor.left, anchor.top, moved]);

  const startDrag = (event: React.PointerEvent) => {
    if (event.button !== 0) return;
    const from = placement;
    const startX = event.clientX;
    const startY = event.clientY;
    setDragging(true);
    const move = (moveEvent: PointerEvent) => {
      const panel = ref.current;
      if (!panel) return;
      setMoved(
        clampToViewport(
          {
            left: from.left + moveEvent.clientX - startX,
            top: from.top + moveEvent.clientY - startY,
          },
          panel.offsetWidth,
          panel.offsetHeight,
        ),
      );
    };
    const end = () => {
      setDragging(false);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
    // Otherwise the pointer selects the heading text on the way past.
    event.preventDefault();
  };

  return { ref, placement, dragging, dragHandleProps: { onPointerDown: startDrag } };
}
