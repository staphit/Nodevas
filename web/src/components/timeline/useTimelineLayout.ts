/**
 * Where each day sits on the timeline.
 *
 * Records are bucketed by node and day, empty days are optionally collapsed
 * into gaps, and the result is a day-index → offset table. Cards, gates and
 * stamps all read that table, so collapsing can never leave them misaligned
 * with the grid.
 */

import { useMemo } from "react";
import type { Graph, HistoryEvent, PlanMilestone, RunState } from "../../types";
import { dayKey, parseLocalDay, type Col } from "../canvas/geometry";
import type { TimelineRowItem } from "./TimelineGrid";

/** Gap rows stand in for a run of empty days, at a fixed small height. */
export const GAP_H = 20;
const HORIZONTAL_HEADER_H = 48;
const HORIZONTAL_GAP_W = 56;

export function useTimelineLayout({
  days,
  runState,
  graph,
  collapseEmpty,
  timelineCols,
  timelineCellWidth,
  timelineCellHeight,
  boardViewport,
  zoom,
  todayKey,
  nowOffset,
}: {
  days: Date[];
  runState: RunState | null;
  graph: Graph | null;
  collapseEmpty: boolean;
  timelineCols: Col[];
  timelineCellWidth: number;
  timelineCellHeight: number;
  boardViewport: { width: number; height: number };
  zoom: number;
  todayKey: string;
  nowOffset: number;
}) {
  const byNodeDay = useMemo(() => {
    const map = new Map<string, Map<string, HistoryEvent[]>>();
    for (const ev of runState?.history ?? []) {
      if (ev.event !== "status" || !ev.node) continue;
      const d = new Date(ev.t);
      if (isNaN(d.getTime())) continue;
      const key = dayKey(d);
      let dayMap = map.get(ev.node);
      if (!dayMap) map.set(ev.node, (dayMap = new Map()));
      let list = dayMap.get(key);
      if (!list) dayMap.set(key, (list = []));
      list.push(ev);
    }
    // Minute-level ordering inside each day cell.
    for (const dayMap of map.values()) {
      for (const list of dayMap.values()) {
        list.sort((a, b) => a.t.localeCompare(b.t));
      }
    }
    return map;
  }, [runState]);

  const plansByNodeDay = useMemo(() => {
    const map = new Map<string, Map<string, PlanMilestone[]>>();
    for (const [nodeId, plans] of Object.entries(graph?.ui?.plans ?? {})) {
      const dayMap = new Map<string, PlanMilestone[]>();
      for (const plan of plans) {
        if (!parseLocalDay(plan.date)) continue;
        dayMap.set(plan.date, [...(dayMap.get(plan.date) ?? []), plan]);
      }
      // All-day milestones first, then by time (minute precision).
      for (const list of dayMap.values()) {
        list.sort((a, b) => (a.time ?? "").localeCompare(b.time ?? ""));
      }
      map.set(nodeId, dayMap);
    }
    return map;
  }, [graph]);

  const recordDayKeys = useMemo(() => {
    const keys = new Set<string>();
    for (const date of days) {
      const key = dayKey(date);
      const hasActual = [...byNodeDay.values()].some(
        (dayMap) => (dayMap.get(key)?.length ?? 0) > 0,
      );
      const hasPlan = [...plansByNodeDay.values()].some(
        (dayMap) => (dayMap.get(key)?.length ?? 0) > 0,
      );
      if (hasActual || hasPlan) keys.add(key);
    }
    return keys;
  }, [byNodeDay, days, plansByNodeDay]);
  const rowItems: TimelineRowItem[] = useMemo(() => {
    if (!collapseEmpty)
      return days.map((d, i) => ({ kind: "day" as const, date: d, dayIndex: i }));
    const out: TimelineRowItem[] = [];
    let gap = 0;
    days.forEach((d, i) => {
      if (recordDayKeys.has(dayKey(d))) {
        if (gap > 0) {
          out.push({ kind: "gap", count: gap });
          gap = 0;
        }
        out.push({ kind: "day", date: d, dayIndex: i });
      } else {
        gap++;
      }
    });
    if (gap > 0) out.push({ kind: "gap", count: gap });
    return out;
  }, [collapseEmpty, days, recordDayKeys]);

  const horizontalItemWidth = (item: TimelineRowItem) =>
    item.kind === "day" ? timelineCellWidth : HORIZONTAL_GAP_W;
  const horizontalContentW =
    timelineCellWidth +
    rowItems.reduce((width, item) => width + horizontalItemWidth(item), 0);
  const horizontalTimelineW = Math.max(
    horizontalContentW,
    boardViewport.width / zoom,
  );
  const horizontalTimelineH = Math.max(
    HORIZONTAL_HEADER_H +
      Math.max(timelineCols.length, 1) * timelineCellHeight,
    boardViewport.height / zoom,
  );
  let horizontalNowX: number | null = null;
  let horizontalCursorX = timelineCellWidth;
  for (const item of rowItems) {
    if (item.kind === "day" && dayKey(item.date) === todayKey) {
      horizontalNowX =
        horizontalCursorX +
        (nowOffset / timelineCellHeight) * horizontalItemWidth(item);
      break;
    }
    horizontalCursorX += horizontalItemWidth(item);
  }

  // Visible-layout mapping: day index -> rendered y offset. Cards, gates
  // and stamps all use this, so collapsing never breaks grid alignment.
  const { dayY, totalH } = useMemo(() => {
    const dayY: number[] = new Array(days.length).fill(0);
    let y = 0;
    let di = 0;
    for (const item of rowItems) {
      if (item.kind === "day") {
        dayY[item.dayIndex] = y;
        di = item.dayIndex + 1;
        y += timelineCellHeight;
      } else {
        for (let k = 0; k < item.count; k++) {
          dayY[di + k] = y;
        }
        di += item.count;
        y += GAP_H;
      }
    }
    return { dayY, totalH: y };
  }, [rowItems, days.length, timelineCellHeight]);

  return {
    byNodeDay,
    plansByNodeDay,
    rowItems,
    dayY,
    totalH,
    horizontalTimelineW,
    horizontalTimelineH,
    horizontalNowX,
  };
}
