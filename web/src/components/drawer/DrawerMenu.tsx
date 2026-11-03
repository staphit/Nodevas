/**
 * The node panel's dropdowns: more formatting, the page switcher, the
 * dispatch caret and the panel's own `⋯`. Each is a button with a visible
 * label that opens a menu of items, optionally followed by a few controls that
 * are not menu items (the table size form, the text size stepper).
 *
 * Same keyboard contract as the board's OverflowMenu, which this differs from
 * only in taking any trigger content and radio items: the first item takes
 * focus on open, the arrow keys cycle the items, Escape closes and hands focus
 * back to the trigger — and is claimed, so the panel underneath does not also
 * read it as "close me". An outside press closes without stealing focus back.
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

import { clampToViewport } from "../floatingPanel";

export type DrawerMenuItem = {
  id: string;
  label: string;
  icon?: ReactNode;
  /** Secondary text on the right: a shortcut, a format, a count. */
  hint?: string;
  onSelect: () => void;
  /**
   * Set for one of a set of choices (a page, an editing mode): drawn as a
   * radio item with this as its state — or, with `toggle`, as an on/off
   * checkbox item. Choosing either still closes the menu.
   */
  checked?: boolean;
  toggle?: boolean;
  disabled?: boolean;
  title?: string;
  danger?: boolean;
  /** Starts a new group: a separator is drawn above it. */
  separatorBefore?: boolean;
  /** A heading above the group this item starts; not focusable. */
  group?: string;
};

const UNPLACED: CSSProperties = { position: "fixed", left: 0, top: 0, visibility: "hidden" };
const ITEM_SELECTOR =
  '[role="menuitem"]:not(:disabled), [role="menuitemradio"]:not(:disabled), [role="menuitemcheckbox"]:not(:disabled)';

export function DrawerMenu({
  label,
  trigger,
  triggerClassName = "tool-btn",
  triggerTitle,
  className = "",
  items,
  align = "start",
  disabled = false,
  children,
}: {
  /** Names the trigger and the menu for assistive tech. */
  label: string;
  /** What the trigger shows. Without it the label is the visible text. */
  trigger?: ReactNode;
  triggerClassName?: string;
  triggerTitle?: string;
  className?: string;
  items: DrawerMenuItem[];
  /** Which edge of the trigger the panel lines up with. */
  align?: "start" | "end";
  disabled?: boolean;
  /** Controls under the items; handed the close function for forms. */
  children?: (close: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [panelStyle, setPanelStyle] = useState<CSSProperties | undefined>(undefined);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);

  const closeAndReturnFocus = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  const menuItems = useCallback(
    () => Array.from(panelRef.current?.querySelectorAll<HTMLButtonElement>(ITEM_SELECTOR) ?? []),
    [],
  );

  const ready = open && panelStyle !== undefined;
  useEffect(() => {
    if (!ready) return;
    // The checked choice first, so reopening a switcher lands on where you are.
    const items = menuItems();
    (
      items.find(
        (item) =>
          item.getAttribute("role") === "menuitemradio" &&
          item.getAttribute("aria-checked") === "true",
      ) ?? items[0]
    )?.focus();
  }, [ready, menuItems]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      event.preventDefault();
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

  // Fixed to the window: the toolbar and the panel both clip and scroll.
  useLayoutEffect(() => {
    if (!open) {
      setPanelStyle(undefined);
      return;
    }
    const place = () => {
      const trigger = triggerRef.current;
      const panel = panelRef.current;
      if (!trigger || !panel) return;
      const rect = trigger.getBoundingClientRect();
      const left = align === "end" ? rect.right - panel.offsetWidth : rect.left;
      const point = clampToViewport(
        { left, top: rect.bottom + 4 },
        panel.offsetWidth,
        panel.offsetHeight,
      );
      setPanelStyle({ position: "fixed", left: point.left, top: point.top });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open, align]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const keys = ["ArrowDown", "ArrowUp", "Home", "End"];
    if (!keys.includes(event.key)) return;
    const buttons = menuItems();
    if (buttons.length === 0) return;
    event.preventDefault();
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? buttons.length - 1
          : (at + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
    buttons[next]?.focus();
  };

  return (
    <div className={`drawer-menu ${className}`.trim()} ref={rootRef}>
      <button
        type="button"
        ref={triggerRef}
        className={triggerClassName}
        title={triggerTitle ?? label}
        aria-label={trigger ? label : undefined}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((was) => !was)}
      >
        {trigger ?? label}
      </button>
      {open && (
        <div className="drawer-menu-panel" ref={panelRef} style={panelStyle ?? UNPLACED}>
          <div role="menu" aria-label={label} onKeyDown={onMenuKeyDown}>
            {items.map((item) => {
              const radio = item.checked !== undefined;
              const role = !radio ? "menuitem" : item.toggle ? "menuitemcheckbox" : "menuitemradio";
              return [
                item.separatorBefore ? (
                  <div key={`${item.id}-separator`} role="separator" className="drawer-menu-separator" />
                ) : null,
                item.group ? (
                  <div key={`${item.id}-group`} className="drawer-menu-group" aria-hidden="true">
                    {item.group}
                  </div>
                ) : null,
                <button
                  key={item.id}
                  type="button"
                  role={role}
                  aria-checked={radio ? item.checked : undefined}
                  className={`drawer-menu-item${item.checked ? " checked" : ""}${
                    item.danger ? " danger" : ""
                  }`}
                  disabled={item.disabled}
                  title={item.title}
                  onClick={() => {
                    closeAndReturnFocus();
                    item.onSelect();
                  }}
                >
                  <span className="drawer-menu-icon" aria-hidden="true">
                    {item.checked ? "✓" : radio && !item.toggle ? "" : item.icon}
                  </span>
                  <span className="drawer-menu-label">{item.label}</span>
                  {item.hint && <span className="drawer-menu-hint">{item.hint}</span>}
                </button>,
              ];
            })}
          </div>
          {children && <div className="drawer-menu-extra">{children(closeAndReturnFocus)}</div>}
        </div>
      )}
    </div>
  );
}
