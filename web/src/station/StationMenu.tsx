// Station's small menus: the "⋯" on each row of "In progress" and the view
// picker in its header. Like the app's overflow menu (components/menus), but
// with a trigger of the caller's own (a label, not only the ⋯ icon) and
// radio items for a choice of one. The first item takes focus, the arrows
// cycle, Escape and a choice hand focus back, an outside click closes.

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

import { clampToViewport } from "../components/floatingPanel";

export interface StationMenuItem {
  id: string;
  label: string;
  onSelect: () => void;
  /** A choice of one (the view picker): drawn as a radio item. */
  selected?: boolean;
  /** An on/off setting: drawn as a checkbox item. */
  checked?: boolean;
  disabled?: boolean;
  title?: string;
  /** Drawn in the danger colour: stops or cancels something. */
  danger?: boolean;
  separatorBefore?: boolean;
}

const UNPLACED: CSSProperties = { position: "fixed", left: 0, top: 0, visibility: "hidden" };
const ITEM_SELECTOR = '[role^="menuitem"]:not(:disabled)';

export function StationMenu({
  items,
  label,
  trigger,
  triggerClassName = "station-menu-trigger",
}: {
  items: StationMenuItem[];
  /** Names the menu, and the trigger when it shows only an icon. */
  label: string;
  /** What the trigger shows; the label is its accessible name either way. */
  trigger: ReactNode;
  triggerClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  // Undefined until the panel is placed: it shows hidden till then, and the
  // first item takes focus only once it is visible (a hidden button cannot).
  const [style, setStyle] = useState<CSSProperties | undefined>(undefined);
  const ready = open && style !== undefined;
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const menuId = useId();

  const close = useCallback((returnFocus: boolean) => {
    setOpen(false);
    if (returnFocus) triggerRef.current?.focus();
  }, []);

  const buttons = () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>(ITEM_SELECTOR) ?? []);

  // Right-aligned under the trigger, kept on screen; placed before paint.
  useLayoutEffect(() => {
    if (!open) {
      setStyle(undefined);
      return;
    }
    const place = () => {
      const anchor = triggerRef.current?.getBoundingClientRect();
      const menu = menuRef.current;
      if (!anchor || !menu) return;
      const point = clampToViewport(
        { left: anchor.right - menu.offsetWidth, top: anchor.bottom + 4 },
        menu.offsetWidth,
        menu.offsetHeight,
      );
      setStyle({ position: "fixed", left: point.left, top: point.top });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open]);

  useEffect(() => {
    if (ready) buttons()[0]?.focus();
  }, [ready]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      close(true);
    };
    const onDown = (event: PointerEvent) => {
      if (event.target instanceof Node && rootRef.current?.contains(event.target)) return;
      close(false);
    };
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("pointerdown", onDown, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("pointerdown", onDown, true);
    };
  }, [open, close]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Tab") {
      close(false);
      return;
    }
    const list = buttons();
    if (!list.length) return;
    const at = list.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (event.key === "ArrowDown") next = (at + 1) % list.length;
    else if (event.key === "ArrowUp") next = (at - 1 + list.length) % list.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = list.length - 1;
    if (next < 0) return;
    event.preventDefault();
    list[next].focus();
  };

  return (
    <div className="station-menu" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className={triggerClassName}
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((was) => !was)}
      >
        {trigger}
      </button>
      {open && (
        <div
          id={menuId}
          ref={menuRef}
          className="station-menu-panel"
          role="menu"
          aria-label={label}
          style={style ?? UNPLACED}
          onKeyDown={onMenuKeyDown}
        >
          {items.map((item) => {
            const role =
              item.selected !== undefined ? "menuitemradio" : item.checked !== undefined ? "menuitemcheckbox" : "menuitem";
            const on = item.selected ?? item.checked;
            return [
              item.separatorBefore ? <div key={`${item.id}-sep`} role="separator" className="station-menu-sep" /> : null,
              <button
                key={item.id}
                type="button"
                role={role}
                aria-checked={role === "menuitem" ? undefined : Boolean(on)}
                className={`station-menu-item${item.danger ? " is-danger" : ""}`}
                disabled={item.disabled}
                title={item.title}
                onClick={() => {
                  close(true);
                  item.onSelect();
                }}
              >
                <span>{item.label}</span>
                {role !== "menuitem" && (
                  <span className="station-menu-check" aria-hidden="true">
                    {on ? "✓" : ""}
                  </span>
                )}
              </button>,
            ];
          })}
        </div>
      )}
    </div>
  );
}
