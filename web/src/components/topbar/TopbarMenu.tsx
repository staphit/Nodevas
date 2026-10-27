/**
 * A menu that hangs off a top bar button: the project menu and the account
 * menu. Same keyboard contract as the shared `OverflowMenu` (focus moves in on
 * open, the arrow keys cycle, Escape closes and hands focus back), but the
 * trigger is whatever the bar needs it to be — a project name, an avatar —
 * rather than a fixed `⋯`, and items can be headings, groups or a checked
 * choice. A disabled item stays focusable (aria-disabled), so a keyboard or a
 * screen reader still reaches it and hears why; pressing it does nothing.
 */

import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";

export interface TopbarMenuItem {
  kind?: "item";
  id: string;
  label: string;
  /** Quieter text after the label: a count, a path. */
  detail?: string;
  icon?: ReactNode;
  onSelect: () => void;
  /** Set for one of a set of choices (a project, a workspace): drawn as a
   * radio item, checked for the current one. */
  checked?: boolean;
  /** Shown and focusable, but pressing it does nothing (aria-disabled). */
  disabled?: boolean;
  title?: string;
  /** Indent steps, for a project inside a folder. */
  depth?: number;
  /** Starts a new group: a separator is drawn above it. */
  separatorBefore?: boolean;
}

export type TopbarMenuEntry =
  | TopbarMenuItem
  | {
      kind: "heading";
      id: string;
      label: string;
      separatorBefore?: boolean;
    }
  | {
      /** A set of items under a heading (the projects, the workspaces): one
       * group, named by the heading, for assistive tech. */
      kind: "group";
      id: string;
      label: string;
      items: TopbarMenuItem[];
      separatorBefore?: boolean;
    };

export interface TopbarMenuProps {
  entries: TopbarMenuEntry[];
  /** Names the trigger and the menu for assistive tech. */
  label: string;
  /** Tooltip; the label when not given. */
  title?: string;
  /** The trigger's visible content. */
  trigger: ReactNode;
  /** Root class; the trigger and panel add theirs to it. */
  className?: string;
  triggerClassName?: string;
  /** Which edge of the trigger the panel lines up with. */
  align?: "start" | "end";
  /** Passed through to the trigger, for the tour's anchors. */
  dataTour?: string;
}

// Disabled items included: they are aria-disabled, and still reached.
const ITEM_SELECTOR = '[role="menuitem"], [role="menuitemradio"]';

export function TopbarMenu({
  entries,
  label,
  title,
  trigger,
  className = "",
  triggerClassName = "",
  align = "start",
  dataTour,
}: TopbarMenuProps) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const menuId = useId();

  const close = useCallback((returnFocus: boolean) => {
    setOpen(false);
    if (returnFocus) triggerRef.current?.focus();
  }, []);

  const items = useCallback(
    () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>(ITEM_SELECTOR) ?? []),
    [],
  );

  // Focus lands on the current choice when there is one (the project the
  // board shows), else on the first item, so a keyboard is inside at once.
  useEffect(() => {
    if (!open) return;
    const buttons = items();
    const current = buttons.find((button) => button.getAttribute("aria-checked") === "true");
    (current ?? buttons[0])?.focus();
  }, [open, items]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Claimed so the board and dialogs below do not read it as theirs.
      event.stopPropagation();
      close(true);
    };
    // An outside press closes without taking focus back: it already put the
    // caret where the user aimed.
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && rootRef.current?.contains(target)) return;
      close(false);
    };
    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, [open, close]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Tab") {
      // Leaving by Tab leaves the menu behind too.
      close(false);
      return;
    }
    const buttons = items();
    if (buttons.length === 0) return;
    const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (event.key === "ArrowDown") next = (at + 1) % buttons.length;
    else if (event.key === "ArrowUp") next = (at - 1 + buttons.length) % buttons.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = buttons.length - 1;
    if (next < 0) return;
    event.preventDefault();
    buttons[next]?.focus();
  };

  const renderItem = (entry: TopbarMenuItem) => {
    const choice = entry.checked !== undefined;
    return (
      <button
        key={entry.id}
        type="button"
        role={choice ? "menuitemradio" : "menuitem"}
        aria-checked={choice ? entry.checked : undefined}
        aria-disabled={entry.disabled ? "true" : undefined}
        className={`topbar-menu-item${entry.checked ? " checked" : ""}`}
        style={entry.depth ? { paddingInlineStart: 10 + entry.depth * 14 } : undefined}
        title={entry.title}
        onClick={() => {
          if (entry.disabled) return;
          close(true);
          entry.onSelect();
        }}
      >
        {choice ? (
          <span className="topbar-menu-check" aria-hidden>
            {entry.checked ? "✓" : ""}
          </span>
        ) : (
          entry.icon
        )}
        <span className="topbar-menu-label">{entry.label}</span>
        {entry.detail && <span className="topbar-menu-detail">{entry.detail}</span>}
      </button>
    );
  };

  return (
    <div className={`topbar-menu ${className}`.trim()} ref={rootRef}>
      <button
        type="button"
        ref={triggerRef}
        className={`topbar-menu-trigger ${triggerClassName}`.trim()}
        onClick={() => setOpen((was) => !was)}
        title={title ?? label}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        data-tour={dataTour}
      >
        {trigger}
      </button>
      {open && (
        <div
          id={menuId}
          ref={menuRef}
          className={`topbar-menu-panel align-${align}`}
          role="menu"
          aria-label={label}
          onKeyDown={onMenuKeyDown}
        >
          {entries.map((entry) => {
            const separator = entry.separatorBefore ? (
              <div key={`${entry.id}-separator`} role="separator" className="topbar-menu-separator" />
            ) : null;
            if (entry.kind === "heading") {
              return [
                separator,
                <div key={entry.id} className="topbar-menu-heading" role="presentation">
                  {entry.label}
                </div>,
              ];
            }
            if (entry.kind === "group") {
              return [
                separator,
                <div key={entry.id} role="group" aria-label={entry.label} className="topbar-menu-group">
                  {/* The group's name already says it to assistive tech. */}
                  <div className="topbar-menu-heading" aria-hidden="true">
                    {entry.label}
                  </div>
                  {entry.items.map((item) => [
                    item.separatorBefore ? (
                      <div key={`${item.id}-separator`} role="separator" className="topbar-menu-separator" />
                    ) : null,
                    renderItem(item),
                  ])}
                </div>,
              ];
            }
            return [separator, renderItem(entry)];
          })}
        </div>
      )}
    </div>
  );
}
