/**
 * Dragging a milestone to another day.
 *
 * Two records move the same way but mean different things: an expected
 * milestone is a plan being rescheduled, an actual stamp is history being
 * corrected. Both are pointer drags over the timeline grid, so they share the
 * day hit-testing and the edge auto-scroll and differ only in what they write.
 */

import { useRef, useState } from "react";
import { reportError } from "../../store";
import { statusTheme } from "../../statusTheme";
import { planOrderError, planStatusLabel } from "../../plan";
import type {
  Graph,
  HistoryEvent,
  PlanMilestone,
  PlanStatus,
  PlanStatusDefinition,
  Status,
  StatusDefinition,
} from "../../types";
import type { PlanCommand } from "../../domain/commands";
import type { CommandResult } from "../../state/operations";
import { dayKey, parseLocalDay } from "../canvas/geometry";
import type { LaneContextMenu } from "../LaneView";

export interface PlanDragState {
  pointerId: number;
  nodeId: string;
  status: PlanStatus;
  fromDate: string;
  targetDate: string;
  x: number;
  y: number;
  dx: number;
  dy: number;
  moved: boolean;
}

export interface ActualDragState {
  pointerId: number;
  eventId: string;
  nodeId: string;
  status: Status;
  eventTime: string;
  fromDate: string;
  targetDate: string;
  x: number;
  y: number;
  dx: number;
  dy: number;
  moved: boolean;
}

export function useMilestoneDrag({
  graph,
  zoom,
  boardRef,
  timelineOrientation,
  planStatusDefinitions,
  customStatuses,
  updatePlan,
  moveStamp,
  selectNode,
  openTab,
  setContextMenu,
  setPlanNotice,
  canEdit = true,
}: {
  graph: Graph | null;
  zoom: number;
  boardRef: React.RefObject<HTMLDivElement | null>;
  timelineOrientation: "vertical" | "horizontal";
  planStatusDefinitions: PlanStatusDefinition[];
  customStatuses: StatusDefinition[];
  updatePlan: (command: PlanCommand) => Promise<CommandResult>;
  moveStamp: (eventId: string, at: string) => Promise<unknown>;
  selectNode: (nodeId: string) => void;
  openTab: (id: string) => Promise<unknown>;
  setContextMenu: (menu: LaneContextMenu | null) => void;
  setPlanNotice: (notice: { text: string; kind: "ok" | "error" } | null) => void;
  /** False for a read-only session: a milestone cannot be dragged to a new date. */
  canEdit?: boolean;
}) {
  const planDragRef = useRef<PlanDragState | null>(null);
  const [planDrag, setPlanDrag] = useState<PlanDragState | null>(null);
  const actualDragRef = useRef<ActualDragState | null>(null);
  const [actualDrag, setActualDrag] = useState<ActualDragState | null>(null);

  const startPlanDrag = (
    event: React.PointerEvent<HTMLDivElement>,
    nodeId: string,
    plan: PlanMilestone,
  ) => {
    if (!canEdit) return;
    if (event.pointerType === "mouse" && event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    setContextMenu(null);
    event.currentTarget.setPointerCapture(event.pointerId);
    const next = {
      pointerId: event.pointerId,
      nodeId,
      status: plan.status,
      fromDate: plan.date,
      targetDate: plan.date,
      x: event.clientX,
      y: event.clientY,
      dx: 0,
      dy: 0,
      moved: false,
    };
    planDragRef.current = next;
    setPlanDrag(next);
  };

  const dateAtPointer = (clientX: number, clientY: number, fallback: string) => {
    const selector =
      timelineOrientation === "horizontal"
        ? ".horizontal-date-cell[data-date]"
        : ".board-row[data-date]";
    for (
      const row of boardRef.current?.querySelectorAll<HTMLElement>(selector) ?? []
    ) {
      const rect = row.getBoundingClientRect();
      const matches =
        timelineOrientation === "horizontal"
          ? clientX >= rect.left && clientX <= rect.right
          : clientY >= rect.top && clientY <= rect.bottom;
      if (matches) {
        return row.dataset.date ?? fallback;
      }
    }
    return fallback;
  };

  const autoScrollTimeline = (clientX: number, clientY: number) => {
    const board = boardRef.current;
    if (!board) return;
    const rect = board.getBoundingClientRect();
    if (timelineOrientation === "horizontal") {
      if (clientX < rect.left + 72) board.scrollLeft -= 18;
      if (clientX > rect.right - 40) board.scrollLeft += 18;
      return;
    }
    if (clientY < rect.top + 40) board.scrollTop -= 16;
    if (clientY > rect.bottom - 40) board.scrollTop += 16;
  };

  const movePlanDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    const current = planDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    const screenDx = event.clientX - current.x;
    const screenDy = event.clientY - current.y;
    const targetDate = dateAtPointer(
      event.clientX,
      event.clientY,
      current.targetDate,
    );
    autoScrollTimeline(event.clientX, event.clientY);
    const next = {
      ...current,
      dx: screenDx / zoom,
      dy: screenDy / zoom,
      targetDate,
      moved: current.moved || Math.abs(screenDx) + Math.abs(screenDy) > 5,
    };
    planDragRef.current = next;
    setPlanDrag(next);
  };

  const endPlanDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    const current = planDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    planDragRef.current = null;
    setPlanDrag(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (!current.moved) {
      selectNode(current.nodeId);
      void openTab(current.nodeId).catch(reportError);
      return;
    }
    if (current.targetDate === current.fromDate) return;
    const plans = graph?.ui?.plans?.[current.nodeId] ?? [];
    const error = planOrderError(plans, current.status, current.targetDate);
    if (error) {
      setPlanNotice({ text: error, kind: "error" });
      return;
    }
    void updatePlan({
      type: "plan.move",
      nodeId: current.nodeId,
      milestoneType: current.status,
      date: current.targetDate,
    }).then((result) => {
      if (!result.ok) {
        setPlanNotice({ text: result.message, kind: "error" });
        return;
      }
      setPlanNotice({
        text: `${planStatusLabel(current.status, planStatusDefinitions)}已移至 ${
          current.targetDate
        }`,
        kind: "ok",
      });
    });
  };

  const startActualDrag = (
    event: React.PointerEvent<HTMLDivElement>,
    historyEvent: HistoryEvent,
  ) => {
    if (!canEdit) return;
    if (event.pointerType === "mouse" && event.button !== 0) return;
		if (!historyEvent.id || !historyEvent.node) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    selectNode(historyEvent.node);
    setContextMenu(null);
    event.currentTarget.setPointerCapture(event.pointerId);
    const fromDate = dayKey(new Date(historyEvent.t));
    const next = {
      pointerId: event.pointerId,
      eventId: historyEvent.id,
      nodeId: historyEvent.node,
      status: (historyEvent.to ?? "ready") as Status,
      eventTime: historyEvent.t,
      fromDate,
      targetDate: fromDate,
      x: event.clientX,
      y: event.clientY,
      dx: 0,
      dy: 0,
      moved: false,
    };
    actualDragRef.current = next;
    setActualDrag(next);
  };

  const moveActualDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    const current = actualDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    const screenDx = event.clientX - current.x;
    const screenDy = event.clientY - current.y;
    const next = {
      ...current,
      dx: screenDx / zoom,
      dy: screenDy / zoom,
      targetDate: dateAtPointer(
        event.clientX,
        event.clientY,
        current.targetDate,
      ),
      moved: current.moved || Math.abs(screenDx) + Math.abs(screenDy) > 5,
    };
    actualDragRef.current = next;
    setActualDrag(next);
    autoScrollTimeline(event.clientX, event.clientY);
  };

  const endActualDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    const current = actualDragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    event.preventDefault();
    event.stopPropagation();
    actualDragRef.current = null;
    setActualDrag(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (!current.moved) {
      void openTab(current.nodeId).catch(reportError);
      return;
    }
    if (current.targetDate === current.fromDate) return;
    const targetDay = parseLocalDay(current.targetDate);
    const sourceTime = new Date(current.eventTime);
    if (!targetDay || Number.isNaN(sourceTime.getTime())) {
      setPlanNotice({ text: "無法解析實際紀錄日期", kind: "error" });
      return;
    }
    targetDay.setHours(
      sourceTime.getHours(),
      sourceTime.getMinutes(),
      sourceTime.getSeconds(),
      sourceTime.getMilliseconds(),
    );
    void moveStamp(current.eventId, targetDay.toISOString())
      .then(() =>
        setPlanNotice({
          text: `實際${statusTheme(current.status, customStatuses).label}已移至 ${current.targetDate}，異動已寫入歷史`,
          kind: "ok",
        }),
      )
      .catch((error) => {
        reportError(error);
        setPlanNotice({ text: (error as Error).message, kind: "error" });
      });
  };

  return {
    planDrag,
    actualDrag,
    startPlanDrag,
    movePlanDrag,
    endPlanDrag,
    startActualDrag,
    moveActualDrag,
    endActualDrag,
  };
}
