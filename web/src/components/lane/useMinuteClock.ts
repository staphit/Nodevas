/**
 * The time of day, to the minute, for the timeline's current-time line.
 *
 * The line is labelled to the minute, so it moves on each minute — on the
 * minute, not a minute after the board came up. A hidden page has no one
 * reading it: the clock stops there and catches up when the page shows.
 */

import { useEffect, useState } from "react";

const MINUTE = 60_000;

const sameMinute = (left: Date, right: Date) =>
  Math.floor(left.getTime() / MINUTE) === Math.floor(right.getTime() / MINUTE);

export function useMinuteClock(enabled: boolean): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (!enabled) return;
    let timer: number | undefined;
    const tick = () => {
      window.clearTimeout(timer);
      timer = undefined;
      if (document.hidden) return;
      const current = new Date();
      setNow((previous) => (sameMinute(previous, current) ? previous : current));
      timer = window.setTimeout(tick, MINUTE - (current.getTime() % MINUTE));
    };
    tick();
    document.addEventListener("visibilitychange", tick);
    window.addEventListener("focus", tick);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", tick);
      window.removeEventListener("focus", tick);
    };
  }, [enabled]);
  return now;
}
