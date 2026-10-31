import type { RunAsk, RunEvent } from "../api";

/** One line of a run's timeline: an event of its runner, or an ask. */
export type TimelineItem = { kind: "event"; event: RunEvent } | { kind: "ask"; ask: RunAsk };

/** Not built yet: the events alone. */
export function interleaveAsks(events: RunEvent[], _asks: RunAsk[] | undefined): TimelineItem[] {
  return events.map((event) => ({ kind: "event", event }));
}
