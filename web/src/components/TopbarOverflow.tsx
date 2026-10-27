/**
 * The top bar's phone-width overflow menu.
 *
 * On a 393px screen the five action buttons are what push the bar onto a
 * second and third row, and they are also the least urgent thing there: the
 * explorer toggle, the project switch and search are used constantly, while
 * settings, backup and the theme are reached a few times a session. So they
 * collapse into one button here and stay untouched everywhere else.
 *
 * The trigger is width-driven, not pointer-driven: a phone-sized window on a
 * desktop has the same problem, and a tablet with a finger and 1024px does
 * not. `useCoarsePointer` answers the wrong question for this.
 *
 * The menu itself is the shared `OverflowMenu`; this file only decides when
 * the top bar needs one.
 */

import { useEffect, useState, type ReactNode } from "react";

import { useI18n } from "../i18n";
import { OverflowMenu } from "./menus/OverflowMenu";

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

export type TopbarOverflowItem = {
  id: string;
  /** Same wording as the button's own label, so the two are one control. */
  label: string;
  icon: ReactNode;
  onSelect: () => void;
};

export function TopbarOverflow({ items }: { items: TopbarOverflowItem[] }) {
  const { t } = useI18n();
  const narrow = useNarrowViewport();

  // Rendered only below the breakpoint, so the desktop bar keeps the five real
  // buttons rather than a menu that hides them behind an extra press.
  if (!narrow) return null;

  return (
    <OverflowMenu
      items={items}
      label={t("topbar.moreActions")}
      className="topbar-overflow"
      triggerClassName="icon-btn"
    />
  );
}
