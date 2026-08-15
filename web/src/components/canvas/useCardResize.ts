import { useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import {
  CARD_HANDLES,
  clampCardHeight,
  clampCardWidth,
  resizeCardByKeyboard,
  resizeCardSize,
  type Rect,
} from "./geometry";
import {
  NO_SNAP_TARGETS,
  SNAP_THRESHOLD_PX,
  buildSnapTargets,
  solveResizeSnap,
  type SnapBox,
  type SnapGuide,
  type SnapTargets,
} from "./snapping";

export type CardResizeHandle = (typeof CARD_HANDLES)[number];

export interface CardResizeState {
  pointerId: number;
  nodeId: string;
  startX: number;
  startY: number;
  startWidth: number;
  startHeight: number;
  dirX: number;
  dirY: number;
  width: number;
  height: number;
  /** Alignment lines the edge being dragged is currently sitting on. */
  guides: SnapGuide[];
}

export function useCardResize({
  zoom,
  onCommit,
  canResize = true,
  snapEnabled = false,
  snapBoxes = [],
  rectOf,
}: {
  zoom: number;
  onCommit: (nodeId: string, size: { width: number; height: number }) => void;
  /** False for a read-only session: the handles do nothing at all. */
  canResize?: boolean;
  /**
   * Whether the edge under the handle aligns to its neighbours. A card is
   * anchored at its centre, so both edges move: the size has to change by twice
   * the pull, and the opposite edge travels the other way.
   */
  snapEnabled?: boolean;
  snapBoxes?: readonly SnapBox[];
  /** The card's box at rest, in the same space as `snapBoxes`. */
  rectOf?: (nodeId: string) => Rect | null;
}) {
  const resizeRef = useRef<CardResizeState | null>(null);
  const resizeTargets = useRef<SnapTargets>(NO_SNAP_TARGETS);
  const resizeCenter = useRef<{ x: number; y: number } | null>(null);
  const [state, setState] = useState<CardResizeState | null>(null);

  const onPointerDown = (
    event: PointerEvent<HTMLElement>,
    nodeId: string,
    dirX: number,
    dirY: number,
    width: number,
    height: number,
  ) => {
    if (event.button !== 0 || !canResize) return;
    event.preventDefault();
    event.stopPropagation();
    try {
      event.currentTarget.setPointerCapture?.(event.pointerId);
    } catch {
      // Pointer capture is unavailable in some test and embedded contexts.
    }
    const box = rectOf?.(nodeId) ?? null;
    resizeTargets.current =
      snapEnabled && box ? buildSnapTargets(snapBoxes, new Set([nodeId])) : NO_SNAP_TARGETS;
    resizeCenter.current = box
      ? { x: (box.left + box.right) / 2, y: (box.top + box.bottom) / 2 }
      : null;
    const next: CardResizeState = {
      pointerId: event.pointerId,
      nodeId,
      startX: event.clientX,
      startY: event.clientY,
      startWidth: width,
      startHeight: height,
      dirX,
      dirY,
      width,
      height,
      guides: [],
    };
    resizeRef.current = next;
    setState(next);
  };

  const onPointerMove = (event: PointerEvent<HTMLElement>) => {
    const current = resizeRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    const raw = resizeCardSize(
      current.startWidth,
      current.startHeight,
      current.dirX,
      current.dirY,
      event.clientX - current.startX,
      event.clientY - current.startY,
      zoom,
    );
    const size = { ...raw, guides: [] as SnapGuide[] };
    const center = resizeCenter.current;
    if (snapEnabled && !event.shiftKey && center) {
      const bounds: Rect = {
        left: center.x - raw.width / 2,
        right: center.x + raw.width / 2,
        top: center.y - raw.height / 2,
        bottom: center.y + raw.height / 2,
      };
      const pull = solveResizeSnap(
        bounds,
        current.dirX,
        current.dirY,
        resizeTargets.current,
        SNAP_THRESHOLD_PX / zoom,
      );
      size.width = clampCardWidth(raw.width + pull.dWidth);
      size.height = clampCardHeight(raw.height + pull.dHeight);
      size.guides = pull.guides;
    }
    const next = { ...current, ...size };
    resizeRef.current = next;
    setState(next);
  };

  const finish = (event: PointerEvent<HTMLElement>) => {
    const current = resizeRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    resizeRef.current = null;
    resizeTargets.current = NO_SNAP_TARGETS;
    resizeCenter.current = null;
    setState(null);
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (current.width === current.startWidth && current.height === current.startHeight) return;
    onCommit(current.nodeId, { width: current.width, height: current.height });
  };

  const onKeyDown = (
    event: KeyboardEvent<HTMLElement>,
    nodeId: string,
    _dirX: number,
    _dirY: number,
    width: number,
    height: number,
  ) => {
    if (!canResize) return;
    const size = resizeCardByKeyboard(width, height, event.key, event.shiftKey);
    if (!size) return;
    event.preventDefault();
    event.stopPropagation();
    onCommit(nodeId, size);
  };

  return {
    state,
    handlers: {
      onPointerDown,
      onPointerMove,
      onPointerUp: finish,
      onPointerCancel: finish,
      onKeyDown,
    },
  };
}
