/**
 * The phone-width breakpoint the shell shares.
 *
 * This file used to hold the top bar's phone-width overflow menu as well. The
 * bar has since shed the buttons that menu stood in for: Settings, help, the
 * tour, the theme and sign-out live in its account menu at every width (see
 * topbar/AccountMenu). What is left is the breakpoint, which the explorer, the
 * drawer, the lane view and the presence cluster still read.
 *
 * The trigger is width-driven, not pointer-driven: a phone-sized window on a
 * desktop has the same problem, and a tablet with a finger and 1024px does
 * not. `useCoarsePointer` answers the wrong question for this.
 */

import { useEffect, useState } from "react";

const NARROW_QUERY = "(max-width: 720px)";

/**
 * Whether the window is phone-width, kept current as that changes: a desktop
 * window is resized across the breakpoint mid-session, and a control that only
 * exists below it has to appear and disappear with the width.
 */
export function useNarrowViewport(): boolean {
  const [narrow, setNarrow] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return false;
    return window.matchMedia(NARROW_QUERY).matches;
  });

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const query = window.matchMedia(NARROW_QUERY);
    const onChange = (event: MediaQueryListEvent) => setNarrow(event.matches);
    setNarrow(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);

  return narrow;
}
