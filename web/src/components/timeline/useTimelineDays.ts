/**
 * How much of the calendar the timeline shows.
 *
 * The range always covers every recorded stamp and every planned milestone,
 * and grows as the user scrolls toward either end — unbounded in both
 * directions. Extending the past shifts the content, so the scroll position
 * is corrected in the same frame to keep the view still. That correction is
 * skipped while empty days are collapsed, since the added days would fold
 * into one gap and re-trigger the extension forever.
 */

import { useMemo, useState } from "react";
import type { Graph, RunState } from "../../types";
import { addLocalDays, parseLocalDay } from "../canvas/geometry";

/** Shortest timeline worth drawing, even for an empty project. */
const MIN_DAYS = 14;

export function useTimelineDays({
  now,
  runState,
  graph,
  isGraph,
  timelineOrientation,
  collapseEmpty,
  timelineCellWidth,
  timelineCellHeight,
  zoom,
}: {
  now: Date;
  runState: RunState | null;
  graph: Graph | null;
  isGraph: boolean;
  timelineOrientation: "vertical" | "horizontal";
  collapseEmpty: boolean;
  timelineCellWidth: number;
  timelineCellHeight: number;
  zoom: number;
}) {
  const [extraDays, setExtraDays] = useState(7);
  const [extraPastDays, setExtraPastDays] = useState(0);
  const days = useMemo(() => {
    const today = new Date(now);
    today.setHours(0, 0, 0, 0);
    let start = addLocalDays(today, -(MIN_DAYS - 1));
    for (const ev of runState?.history ?? []) {
      if (ev.event !== "status") continue;
      const d = new Date(ev.t);
      if (!isNaN(d.getTime())) {
        d.setHours(0, 0, 0, 0);
        if (d < start) start = d;
      }
    }
    let latestPlan: Date | null = null;
    for (const plans of Object.values(graph?.ui?.plans ?? {})) {
      for (const plan of plans) {
        const d = parseLocalDay(plan.date);
        if (!d) continue;
        if (d < start) start = d;
        if (!latestPlan || d > latestPlan) latestPlan = d;
      }
    }
    start = addLocalDays(start, -extraPastDays);
    const out: Date[] = [];
    for (let date = new Date(start); date <= today; date = addLocalDays(date, 1)) {
      out.push(date);
    }
    // Timeline length follows dates and plans, not the graph canvas row where
    // a node happens to be placed.
    const future = Math.max(extraDays, 4);
    for (let i = 0; i < future; i++) {
      out.push(addLocalDays(out[out.length - 1], 1));
    }
    if (latestPlan) {
      const plannedEnd = addLocalDays(latestPlan, 3);
      while (out[out.length - 1] < plannedEnd) {
        out.push(addLocalDays(out[out.length - 1], 1));
      }
    }
    return out;
  }, [runState, graph, extraDays, extraPastDays, now]);

  // near-edge scroll → load more dates (future at the end, past at the start)
  const onBoardScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    const horizontal = !isGraph && timelineOrientation === "horizontal";
    const nearEnd = horizontal
      ? el.scrollLeft + el.clientWidth > el.scrollWidth - 240
      : el.scrollTop + el.clientHeight > el.scrollHeight - 160;
    if (nearEnd) {
      setExtraDays((n) => Math.min(n + 14, 3650));
    }
    // Prepending shifts content, so compensate scroll to keep the view still.
    // Skipped while empty days are collapsed: blank past days would collapse
    // into one gap and re-trigger extension forever.
    const nearStart = horizontal ? el.scrollLeft < 240 : el.scrollTop < 160;
    if (!isGraph && !collapseEmpty && nearStart) {
      setExtraPastDays((n) => {
        const next = Math.min(n + 14, 3650);
        const added = next - n;
        if (added > 0) {
          requestAnimationFrame(() => {
            if (horizontal) el.scrollLeft += added * timelineCellWidth * zoom;
            else el.scrollTop += added * timelineCellHeight * zoom;
          });
        }
        return next;
      });
    }
  };

  return { days, onBoardScroll };
}
