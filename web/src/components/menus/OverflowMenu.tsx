/**
 * The `⋯` overflow menu: one button that holds the actions a bar has no room
 * for, or no reason to show all the time.
 *
 * Grown out of the top bar's phone-width menu (TopbarOverflow), which is now
 * this component behind a width check. The board toolbars use it at every
 * width, for the controls a first-time user does not need to see but a
 * returning one still has to reach: toggles such as snapping and the minimap,
 * and the sliders for card and cell size, which go below the items.
 *
 * Keyboard: the first item takes focus when the menu opens, the arrow keys
 * cycle the items, Escape closes and hands focus back to the trigger.
 */

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

import { IconMore } from "../../icons";
import { clampToViewport } from "../floatingPanel";

export type OverflowMenuItem = {
  id: string;
  /** Same wording as the button it stands in for, so the two are one control. */
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  /**
   * Set for an on/off setting: drawn as a checkbox item, and choosing it keeps
   * the menu open so several can be flipped in one visit.
   */
  checked?: boolean;
  disabled?: boolean;
  title?: string;
  /** Starts a new group: a separator is drawn above it. */
  separatorBefore?: boolean;
};

export interface OverflowMenuProps {
  items: OverflowMenuItem[];
  /** Names the trigger and the menu for assistive tech (and the tooltip). */
  label: string;
  /** Root class; the trigger, panel and items derive theirs from it. */
  className: string;
  triggerClassName?: string;
  /**
   * "anchored" leaves placement to CSS (the top bar's menu hangs off its
   * button). "fixed" pins the panel to the window under the trigger, for
   * triggers inside a strip that clips or scrolls its own overflow.
   */
  placement?: "anchored" | "fixed";
  /** Draws a dot on the trigger: something inside wants attention. */
  alert?: boolean;
  /** Extra controls under the items — sliders and the like, not menu items. */
  children?: ReactNode;
  /**
   * What the trigger shows instead of the `⋯` glyph: words, for a menu that is
   * a named control of its own (the board's Filter, Move or copy) rather than
   * an overflow.
   */
  trigger?: ReactNode;
  /** Which edge of the trigger a fixed panel lines up with. */
  align?: "start" | "end";
  /**
   * A panel of form controls rather than a list of actions: no menu is drawn,
   * the panel is a labelled dialog, and its first field takes focus on open.
   */
  popover?: boolean;
}

/**
 * A fixed panel before it has been measured: out of the flow from its first
 * frame, so it never pushes its own trigger somewhere else while the trigger
 * is being measured, and invisible until it has been placed.
 */
const UNPLACED: CSSProperties = { position: "fixed", left: 0, top: 0, visibility: "hidden" };

const ITEM_SELECTOR = '[role="menuitem"]:not(:disabled), [role="menuitemcheckbox"]:not(:disabled)';

export function OverflowMenu({
  items,
  label,
  className,
  triggerClassName,
  placement = "anchored",
  alert = false,
  children,
  trigger,
  align = "end",
  popover = false,
}: OverflowMenuProps) {
  const [open, setOpen] = useState(false);
  const [panelStyle, setPanelStyle] = useState<CSSProperties | undefined>(undefined);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // Dismissals the user drove — Escape, a choice — hand the keyboard back to
  // where it was. An outside click is left alone: it has already put the
  // caret somewhere the user aimed at, and stealing it back is worse.
  const closeAndReturnFocus = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  const menuItems = useCallback(
    () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>(ITEM_SELECTOR) ?? []),
    [],
  );

  // A menu that opens without focus is unreachable for a keyboard: the trigger
  // is still focused, and the items are somewhere else in the tab order.
  // A fixed panel is focusable only once it has been placed and shown.
  const ready = open && (placement !== "fixed" || panelStyle !== undefined);
  useEffect(() => {
    if (!ready) return;
    if (popover) {
      panelRef.current
        ?.querySelector<HTMLElement>("input, select, textarea, button:not(:disabled)")
        ?.focus();
      return;
    }
    menuItems()[0]?.focus();
  }, [ready, menuItems, popover]);

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

  // Measured before paint, so a fixed panel never flashes at the corner of
  // the window first. Right-aligned under its trigger, then kept on screen.
  useLayoutEffect(() => {
    if (!open || placement !== "fixed") {
      setPanelStyle(undefined);
      return;
    }
    const place = () => {
      const trigger = triggerRef.current;
      const panel = panelRef.current;
      if (!trigger || !panel) return;
      const rect = trigger.getBoundingClientRect();
      const point = clampToViewport(
        {
          left: align === "start" ? rect.left : rect.right - panel.offsetWidth,
          top: rect.bottom + 6,
        },
        panel.offsetWidth,
        panel.offsetHeight,
      );
      setPanelStyle({ position: "fixed", left: point.left, top: point.top });
    };
    place();
    window.addEventListener("resize", place);
    // Capture: the strip the trigger lives in scrolls, not the window.
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open, placement, align]);

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

  const placedStyle = placement === "fixed" ? (panelStyle ?? UNPLACED) : undefined;

  const menu = (
    <div
      className={`${className}-menu`}
      role="menu"
      aria-label={label}
      ref={(element) => {
        menuRef.current = element;
        if (!children) panelRef.current = element;
      }}
      style={children ? undefined : placedStyle}
      onKeyDown={onMenuKeyDown}
    >
      {items.map((item) => {
        const toggle = item.checked !== undefined;
        return [
          item.separatorBefore ? (
            <div key={`${item.id}-separator`} role="separator" className={`${className}-separator`} />
          ) : null,
          <button
            key={item.id}
            type="button"
            role={toggle ? "menuitemcheckbox" : "menuitem"}
            aria-checked={toggle ? item.checked : undefined}
            className={`${className}-item${item.checked ? " checked" : ""}`}
            disabled={item.disabled}
            title={item.title}
            onClick={() => {
              if (toggle) {
                item.onSelect();
                return;
              }
              closeAndReturnFocus();
              item.onSelect();
            }}
          >
            {item.icon}
            <span>{item.label}</span>
            {toggle && (
              <span className={`${className}-check`} aria-hidden>
                {item.checked ? "✓" : ""}
              </span>
            )}
          </button>,
        ];
      })}
    </div>
  );

  return (
    <div className={className} ref={rootRef}>
      <button
        type="button"
        className={`${triggerClassName ?? ""} ${className}-trigger${alert ? " alert" : ""}`.trim()}
        ref={triggerRef}
        onClick={() => setOpen((was) => !was)}
        title={label}
        aria-label={label}
        aria-haspopup={popover ? "dialog" : "menu"}
        aria-expanded={open}
      >
        {trigger ?? <IconMore size={16} />}
      </button>
      {open &&
        (popover ? (
          <div
            className={`${className}-panel`}
            ref={panelRef}
            style={placedStyle}
            role="dialog"
            aria-label={label}
          >
            {children}
          </div>
        ) : children ? (
          <div className={`${className}-panel`} ref={panelRef} style={placedStyle}>
            {menu}
            <div className={`${className}-extra`}>{children}</div>
          </div>
        ) : (
          menu
        ))}
    </div>
  );
}
