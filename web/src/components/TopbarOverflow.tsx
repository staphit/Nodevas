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
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

import { IconMore } from "../icons";
import { useI18n } from "../i18n";

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
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // Dismissals the user drove — Escape, a choice — hand the keyboard back to
  // where it was. An outside click is left alone: it has already put the
  // caret somewhere the user aimed at, and stealing it back is worse.
  const closeAndReturnFocus = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  const menuItems = useCallback(
    () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []),
    [],
  );

  // A menu that opens without focus is unreachable for a keyboard: the trigger
  // is still focused, and the items are somewhere else in the tab order.
  useEffect(() => {
    if (!open) return;
    menuItems()[0]?.focus();
  }, [open, menuItems]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Claimed here so the dialogs and the board below do not also read this
      // Escape as being about them.
      event.stopPropagation();
      closeAndReturnFocus();
    };
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && rootRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, [open, closeAndReturnFocus]);

  // Rendered only below the breakpoint, so the desktop bar keeps the five real
  // buttons rather than a menu that hides them behind an extra press.
  if (!narrow) return null;

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const buttons = menuItems();
    if (buttons.length === 0) return;
    event.preventDefault();
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const step = event.key === "ArrowDown" ? 1 : -1;
    const next = (at + step + buttons.length) % buttons.length;
    buttons[next]?.focus();
  };

  return (
    <div className="topbar-overflow" ref={rootRef}>
      <button
        type="button"
        className="icon-btn topbar-overflow-trigger"
        ref={triggerRef}
        onClick={() => setOpen((was) => !was)}
        title={t("topbar.moreActions")}
        aria-label={t("topbar.moreActions")}
        aria-haspopup="menu"
        aria-expanded={open}
      >
        <IconMore size={16} />
      </button>
      {open && (
        <div
          className="topbar-overflow-menu"
          role="menu"
          aria-label={t("topbar.moreActions")}
          ref={menuRef}
          onKeyDown={onMenuKeyDown}
        >
          {items.map((item) => (
            <button
              key={item.id}
              type="button"
              role="menuitem"
              className="topbar-overflow-item"
              onClick={() => {
                closeAndReturnFocus();
                item.onSelect();
              }}
            >
              {item.icon}
              <span>{item.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
