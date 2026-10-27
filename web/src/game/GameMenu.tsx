// The menu a sprite opens: what can be done with that character, monster or
// boss, one real action per item. Anchored next to the sprite, kept inside
// the window, keyboard-driven like the app's other menus (the first item
// takes focus, arrows cycle, Escape closes and hands focus back).

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { createPortal } from "react-dom";

export interface GameMenuItem {
  id: string;
  label: string;
  onSelect: () => void;
  /** Drawn emphasised: the urgent thing (an ask, a held message). */
  urgent?: boolean;
}

export interface GameMenuState {
  /** The sprite that opened it, for placement and to give focus back. */
  anchor: HTMLElement;
  title: string;
  detail?: string;
  items: GameMenuItem[];
}

const MARGIN = 8;

export function GameMenu({ menu, onClose }: { menu: GameMenuState; onClose: () => void }) {
  const panel = useRef<HTMLDivElement | null>(null);
  const [style, setStyle] = useState<CSSProperties>({ position: "fixed", left: 0, top: 0, visibility: "hidden" });

  useLayoutEffect(() => {
    const box = menu.anchor.getBoundingClientRect();
    const own = panel.current?.getBoundingClientRect();
    const width = own?.width ?? 240;
    const height = own?.height ?? 160;
    const vw = window.innerWidth || 1024;
    const vh = window.innerHeight || 768;
    let left = box.left + box.width / 2 - width / 2;
    let top = box.bottom + 6;
    if (top + height > vh - MARGIN) top = Math.max(MARGIN, box.top - height - 6);
    left = Math.max(MARGIN, Math.min(left, vw - width - MARGIN));
    setStyle({ position: "fixed", left, top });
  }, [menu]);

  useEffect(() => {
    panel.current?.querySelector<HTMLButtonElement>("button")?.focus();
  }, [menu]);

  useEffect(() => {
    const onDown = (event: PointerEvent) => {
      if (panel.current?.contains(event.target as Node)) return;
      if (menu.anchor.contains(event.target as Node)) return;
      onClose();
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onClose();
      menu.anchor.focus();
    };
    document.addEventListener("pointerdown", onDown, true);
    window.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("pointerdown", onDown, true);
      window.removeEventListener("keydown", onKey, true);
    };
  }, [menu, onClose]);

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const buttons = Array.from(panel.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []);
    if (!buttons.length) return;
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (event.key === "ArrowDown") next = (index + 1) % buttons.length;
    else if (event.key === "ArrowUp") next = (index - 1 + buttons.length) % buttons.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = buttons.length - 1;
    else if (event.key === "Tab") {
      onClose();
      return;
    }
    if (next < 0) return;
    event.preventDefault();
    buttons[next].focus();
  };

  return createPortal(
    <div
      ref={panel}
      className="game-menu"
      style={style}
      role="menu"
      aria-label={menu.title}
      onKeyDown={onKeyDown}
    >
      <div className="game-menu-head" aria-hidden="true">
        <strong>{menu.title}</strong>
        {menu.detail && <span>{menu.detail}</span>}
      </div>
      {menu.items.map((item) => (
        <button
          key={item.id}
          type="button"
          role="menuitem"
          className={`game-menu-item${item.urgent ? " urgent" : ""}`}
          onClick={() => {
            onClose();
            item.onSelect();
          }}
        >
          {item.label}
        </button>
      ))}
    </div>,
    document.body,
  );
}
