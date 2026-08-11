/**
 * Dragging a timeline lane into a new position. The gesture is tracked in a
 * ref so pointer events stay cheap, and mirrored into state for the drawing;
 * a drag that never moved is treated as a click and opens the document.
 */

import { useRef, useState } from "react";
import { reportError, type CanvasCommand } from "../../store";
import type { Col } from "../canvas/geometry";
import type { TimelineOrderDrag } from "../timeline/TimelineGrid";

export function useTimelineOrderDrag({
  timelineCols,
  timelineOrientation,
  timelineCellWidth,
  timelineCellHeight,
  zoom,
  selectNode,
  openTab,
  runCanvasCommand,
}: {
  timelineCols: Col[];
  timelineOrientation: "vertical" | "horizontal";
  timelineCellWidth: number;
  timelineCellHeight: number;
  zoom: number;
  selectNode: (nodeId: string) => void;
  openTab: (id: string) => Promise<unknown>;
  runCanvasCommand: (command: CanvasCommand) => void;
}) {
  const timelineOrderDragRef = useRef<TimelineOrderDrag | null>(null);
  const [timelineOrderDrag, setTimelineOrderDrag] = useState(
    timelineOrderDragRef.current,
  );

  const updateTimelineOrderDrag = (next: TimelineOrderDrag | null) => {
    timelineOrderDragRef.current = next;
    setTimelineOrderDrag(next);
  };

  const startTimelineOrderDrag = (
    event: React.PointerEvent<HTMLButtonElement>,
    id: string,
  ) => {
    if (event.button !== 0) return;
    const fromIndex = timelineCols.findIndex((column) => column.node.id === id);
    if (fromIndex < 0) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    updateTimelineOrderDrag({
      pointerId: event.pointerId,
      id,
      fromIndex,
      targetIndex: fromIndex,
      originX: event.clientX,
      originY: event.clientY,
      dx: 0,
      dy: 0,
      moved: false,
    });
  };

  const moveTimelineOrderDrag = (
    event: React.PointerEvent<HTMLButtonElement>,
  ) => {
    const current = timelineOrderDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    const dx = (event.clientX - current.originX) / zoom;
    const dy = (event.clientY - current.originY) / zoom;
    const axisDelta = timelineOrientation === "vertical" ? dx : dy;
    const step =
      timelineOrientation === "vertical"
        ? timelineCellWidth
        : timelineCellHeight;
    const moved = current.moved || Math.abs(axisDelta) > 5;
    const targetIndex = Math.max(
      0,
      Math.min(
        timelineCols.length - 1,
        current.fromIndex + Math.round(axisDelta / step),
      ),
    );
    updateTimelineOrderDrag({ ...current, dx, dy, moved, targetIndex });
  };

  const finishTimelineOrderDrag = (
    event: React.PointerEvent<HTMLButtonElement>,
    commit: boolean,
  ) => {
    const current = timelineOrderDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    updateTimelineOrderDrag(null);
    if (!commit || !current.moved) {
      if (commit && !current.moved) {
        selectNode(current.id);
        void openTab(current.id).catch(reportError);
      }
      return;
    }
    if (current.targetIndex === current.fromIndex) return;
    const order = timelineCols.map((column) => column.node.id);
    const sourceIndex = order.indexOf(current.id);
    if (sourceIndex < 0) return;
    const [nodeID] = order.splice(sourceIndex, 1);
    order.splice(current.targetIndex, 0, nodeID);
    runCanvasCommand({ type: "canvas.setTimelineOrder", order });
  };

  return {
    timelineOrderDrag,
    timelineOrderDragRef,
    updateTimelineOrderDrag,
    startTimelineOrderDrag,
    moveTimelineOrderDrag,
    finishTimelineOrderDrag,
  };
}
