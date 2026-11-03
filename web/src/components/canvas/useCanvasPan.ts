import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent,
  type RefObject,
} from "react";
import {
  clampZoom,
  contentPointFromClient,
  ZOOM_STEP,
  type Point,
} from "./geometry";

interface PanState {
  pointerId: number;
  x: number;
  y: number;
  scrollLeft: number;
  scrollTop: number;
  /**
   * Measured once, when the pan starts: where `.board-inner` was on screen
   * and how far the board can scroll. A pan frame works from these and the
   * scroll it wrote itself, never from a fresh measurement — a read after
   * the last frame's writes is a forced layout of every card on the board.
   */
  inner: { left: number; top: number } | null;
  maxLeft: number;
  maxTop: number;
  /** The scroll this pan last wrote. */
  left: number;
  top: number;
}

/** How far a board can scroll; unbounded where it has no layout to say. */
function scrollRange(board: HTMLElement) {
  const { scrollWidth, scrollHeight, clientWidth, clientHeight } = board;
  return {
    maxLeft: scrollWidth > 0 ? Math.max(0, scrollWidth - clientWidth) : Infinity,
    maxTop: scrollHeight > 0 ? Math.max(0, scrollHeight - clientHeight) : Infinity,
  };
}

function clampScroll(value: number, max: number): number {
  return Math.min(Math.max(0, value), max);
}

/**
 * A pinch in flight. The zoom is recomputed from the distance the fingers
 * started at rather than accumulated per move, so a pinch that goes out and
 * back lands exactly where it began instead of drifting.
 */
interface PinchState {
  pointerIds: [number, number];
  startDistance: number;
  startZoom: number;
  /** The board's place on screen, measured once when the pinch starts. */
  rect: { left: number; top: number };
}

/** Two touches closer together than this have no reliable direction. */
const MIN_PINCH_DISTANCE_PX = 24;

function distance(a: { x: number; y: number }, b: { x: number; y: number }): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

export function useCanvasPan({
  boardRef,
  collapsed,
  displayScale = 1,
  initialZoom = 1,
}: {
  boardRef: RefObject<HTMLDivElement>;
  collapsed?: boolean;
  /** The zoom a fresh mount starts at: 100%, or where the board was left. */
  initialZoom?: number;
  /**
   * A second, independent multiplier the board is drawn at — the graph's node
   * scale. It rides on the same CSS `zoom` the user's zoom does, so every
   * conversion between client pixels and board units divides by the product of
   * the two, never by the zoom alone. The zoom stays the user's own number, so
   * the toolbar keeps reading 100% while the cards are drawn at 150%.
   */
  displayScale?: number;
}) {
  const [zoom, setZoom] = useState(() => clampZoom(initialZoom));
  const viewScale = zoom * displayScale;
  const [panning, setPanning] = useState(false);
  const panRef = useRef<PanState | null>(null);
  // Every pointer currently down on the board, so a second finger can be
  // recognised as the start of a pinch rather than as a competing pan.
  const pointersRef = useRef(new Map<number, { x: number; y: number }>());
  const pinchRef = useRef<PinchState | null>(null);
  // zoomAt closes over the zoom of the render it was created in. A pinch
  // produces many moves between renders, so it reads the live value from here.
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;
  const viewScaleRef = useRef(viewScale);
  viewScaleRef.current = viewScale;
  const zoomAnchorRef = useRef<{
    viewportX: number;
    viewportY: number;
    contentX: number;
    contentY: number;
  } | null>(null);

  // The scale the board was last drawn at, so a change with no anchor of its
  // own — the node-scale slider — can still be re-centred rather than letting
  // the board grow away from whatever the user was looking at.
  const drawnScaleRef = useRef(viewScale);

  useLayoutEffect(() => {
    const board = boardRef.current;
    const previous = drawnScaleRef.current;
    drawnScaleRef.current = viewScale;
    if (!board) return;
    const anchor = zoomAnchorRef.current;
    if (anchor) {
      zoomAnchorRef.current = null;
      board.scrollLeft = anchor.contentX * viewScale - anchor.viewportX;
      board.scrollTop = anchor.contentY * viewScale - anchor.viewportY;
      return;
    }
    if (previous === viewScale || previous <= 0) return;
    const ratio = viewScale / previous;
    const centerX = board.clientWidth / 2;
    const centerY = board.clientHeight / 2;
    board.scrollLeft = (board.scrollLeft + centerX) * ratio - centerX;
    board.scrollTop = (board.scrollTop + centerY) * ratio - centerY;
  }, [boardRef, viewScale]);

  const zoomAt = (
    nextZoom: number,
    clientX?: number,
    clientY?: number,
    measured?: { left: number; top: number },
  ) => {
    const board = boardRef.current;
    if (!board) return;
    const current = zoomRef.current;
    const next = clampZoom(nextZoom);
    if (next === current) return;
    const drawnAt = viewScaleRef.current;
    const rect = measured ?? board.getBoundingClientRect();
    const viewportX = (clientX ?? rect.left + board.clientWidth / 2) - rect.left;
    const viewportY = (clientY ?? rect.top + board.clientHeight / 2) - rect.top;
    zoomAnchorRef.current = {
      viewportX,
      viewportY,
      contentX: (board.scrollLeft + viewportX) / drawnAt,
      contentY: (board.scrollTop + viewportY) / drawnAt,
    };
    setZoom(next);
  };

  const setZoomDirect = (nextZoom: number) => {
    zoomAnchorRef.current = null;
    setZoom(clampZoom(nextZoom));
  };

  /**
   * Zoom so that `point` — board pixels as the new zoom lays the board out,
   * before scaling — sits in the middle of the view. The scroll is written in
   * the commit that draws the new zoom, not in a later frame: a frame can run
   * before that commit, and the zoom's own re-centring would then move it.
   */
  const zoomOnto = (nextZoom: number, point: { x: number; y: number }) => {
    const board = boardRef.current;
    if (!board) return;
    const next = clampZoom(nextZoom);
    const viewportX = board.clientWidth / 2;
    const viewportY = board.clientHeight / 2;
    if (next === zoomRef.current) {
      zoomAnchorRef.current = null;
      board.scrollLeft = point.x * viewScaleRef.current - viewportX;
      board.scrollTop = point.y * viewScaleRef.current - viewportY;
      return;
    }
    zoomAnchorRef.current = { viewportX, viewportY, contentX: point.x, contentY: point.y };
    setZoom(next);
  };

  useEffect(() => {
    const board = boardRef.current;
    if (!board || collapsed) return;
    const onWheel = (event: WheelEvent) => {
      if (!event.altKey || event.deltaY === 0) return;
      event.preventDefault();
      zoomAt(
        zoom + (event.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP),
        event.clientX,
        event.clientY,
      );
    };
    board.addEventListener("wheel", onWheel, { passive: false });
    return () => board.removeEventListener("wheel", onWheel);
  }, [boardRef, collapsed, zoom]);

  /** Ends a pan without touching the pinch bookkeeping. */
  const releasePan = (board: HTMLDivElement, pointerId: number) => {
    panRef.current = null;
    setPanning(false);
    if (board.hasPointerCapture?.(pointerId)) {
      board.releasePointerCapture(pointerId);
    }
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>): boolean => {
    if (event.pointerType === "mouse" && event.button !== 0) return false;
    event.preventDefault();
    const board = event.currentTarget;
    pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY });

    // A second finger turns the gesture into a pinch. Pointer capture is
    // dropped for the first one, because a captured pointer would keep
    // delivering to the pan path and fight the zoom.
    if (pointersRef.current.size === 2) {
      const [first, second] = [...pointersRef.current.entries()];
      const startDistance = distance(first[1], second[1]);
      if (panRef.current) releasePan(board, panRef.current.pointerId);
      if (startDistance >= MIN_PINCH_DISTANCE_PX) {
        const rect = board.getBoundingClientRect();
        pinchRef.current = {
          pointerIds: [first[0], second[0]],
          startDistance,
          startZoom: zoomRef.current,
          rect: { left: rect.left, top: rect.top },
        };
      }
      return true;
    }
    // Three or more fingers is not a gesture this board has; ignore the extras
    // rather than let them restart a pan under an active pinch.
    if (pointersRef.current.size > 2) return true;

    board.setPointerCapture(event.pointerId);
    const scrollLeft = board.scrollLeft;
    const scrollTop = board.scrollTop;
    const inner = board.querySelector<HTMLElement>(".board-inner")?.getBoundingClientRect();
    panRef.current = {
      pointerId: event.pointerId,
      x: event.clientX,
      y: event.clientY,
      scrollLeft,
      scrollTop,
      inner: inner ? { left: inner.left, top: inner.top } : null,
      ...scrollRange(board),
      left: scrollLeft,
      top: scrollTop,
    };
    return true;
  };

  const onPointerMove = (event: PointerEvent<HTMLDivElement>): boolean => {
    const tracked = pointersRef.current.get(event.pointerId);
    if (tracked) {
      tracked.x = event.clientX;
      tracked.y = event.clientY;
    }

    const pinch = pinchRef.current;
    if (pinch) {
      const a = pointersRef.current.get(pinch.pointerIds[0]);
      const b = pointersRef.current.get(pinch.pointerIds[1]);
      if (!a || !b) return true;
      const spread = distance(a, b);
      if (spread < MIN_PINCH_DISTANCE_PX) return true;
      // Anchoring on the midpoint is what makes the content stay under the
      // fingers, so moving both hands pans and spreading them zooms without
      // the two being separate gestures.
      zoomAt(
        pinch.startZoom * (spread / pinch.startDistance),
        (a.x + b.x) / 2,
        (a.y + b.y) / 2,
        pinch.rect,
      );
      return true;
    }

    const pan = panRef.current;
    if (!pan || pan.pointerId !== event.pointerId) return false;
    const dx = event.clientX - pan.x;
    const dy = event.clientY - pan.y;
    if (!panning && Math.abs(dx) + Math.abs(dy) > 3) setPanning(true);
    // Clamped as the browser clamps it, so the scroll kept here is the
    // board's own and pointFromClient can go on without measuring.
    pan.left = clampScroll(pan.scrollLeft - dx, pan.maxLeft);
    pan.top = clampScroll(pan.scrollTop - dy, pan.maxTop);
    event.currentTarget.scrollLeft = pan.left;
    event.currentTarget.scrollTop = pan.top;
    return true;
  };

  const onPointerUp = (event: PointerEvent<HTMLDivElement>): boolean => {
    const wasTracked = pointersRef.current.delete(event.pointerId);
    const pinch = pinchRef.current;
    if (pinch && pinch.pointerIds.includes(event.pointerId)) {
      // The remaining finger does not silently become a pan: lifting one of
      // two fingers usually means the gesture is over, and continuing as a pan
      // would jerk the board by however far that finger has already travelled.
      pinchRef.current = null;
      return true;
    }

    const pan = panRef.current;
    if (!pan || pan.pointerId !== event.pointerId) return wasTracked;
    releasePan(event.currentTarget, event.pointerId);
    return true;
  };

  const cancel = () => {
    if (!panRef.current && !panning && !pinchRef.current) return false;
    panRef.current = null;
    pinchRef.current = null;
    pointersRef.current.clear();
    setPanning(false);
    return true;
  };

  const pointFromClient = (clientX: number, clientY: number): Point | null => {
    // Mid-pan the content has moved by exactly the scroll the pan wrote.
    const pan = panRef.current;
    if (pan?.inner) {
      return contentPointFromClient(
        clientX,
        clientY,
        {
          left: pan.inner.left - (pan.left - pan.scrollLeft),
          top: pan.inner.top - (pan.top - pan.scrollTop),
        },
        viewScale,
      );
    }
    const inner = boardRef.current?.querySelector<HTMLElement>(".board-inner");
    if (!inner) return null;
    return contentPointFromClient(
      clientX,
      clientY,
      inner.getBoundingClientRect(),
      viewScale,
    );
  };

  return {
    state: { zoom, panning, viewScale },
    handlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp,
      onPointerCancel: onPointerUp,
      cancel,
    },
    zoomAt,
    zoomOnto,
    setZoom: setZoomDirect,
    pointFromClient,
  };
}
