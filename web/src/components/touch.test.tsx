import { renderHook } from "@testing-library/react";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LONG_PRESS_MS, LONG_PRESS_SLOP_PX, useTouchContextMenu } from "./touch";

function pointerEvent(
  type: string,
  overrides: { pointerType?: string; pointerId?: number; clientX?: number; clientY?: number } = {},
) {
  // jsdom has no PointerEvent, and the hook only reads the pointer fields, so a
  // MouseEvent carrying them is the same shape as far as the listener is
  // concerned.
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    clientX: overrides.clientX ?? 40,
    clientY: overrides.clientY ?? 60,
  });
  Object.defineProperty(event, "pointerType", { value: overrides.pointerType ?? "touch" });
  Object.defineProperty(event, "pointerId", { value: overrides.pointerId ?? 1 });
  return event;
}

describe("useTouchContextMenu", () => {
  let target: HTMLDivElement;
  let menus: MouseEvent[];

  beforeEach(() => {
    vi.useFakeTimers();
    menus = [];
    target = document.createElement("div");
    target.addEventListener("contextmenu", (event) => menus.push(event as MouseEvent));
    document.body.append(target);
  });

  afterEach(() => {
    vi.useRealTimers();
    target.remove();
  });

  it("opens the context menu after a long press, at the pressed point", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown", { clientX: 120, clientY: 200 }));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(1);
    expect(menus[0].clientX).toBe(120);
    expect(menus[0].clientY).toBe(200);
    // Handlers that check which button opened the menu must see a right click.
    expect(menus[0].button).toBe(2);
  });

  it("bubbles, so a child handler can claim the press from its parent", () => {
    const parentMenus: MouseEvent[] = [];
    const parent = document.createElement("div");
    parent.addEventListener("contextmenu", (event) => parentMenus.push(event as MouseEvent));
    parent.append(target);
    document.body.append(parent);
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(1);
    expect(parentMenus).toHaveLength(1);
    parent.remove();
  });

  it("does not fire when the finger has moved far enough to be a drag", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown", { clientX: 40, clientY: 60 }));
      vi.advanceTimersByTime(LONG_PRESS_MS / 2);
      document.dispatchEvent(
        pointerEvent("pointermove", { clientX: 40 + LONG_PRESS_SLOP_PX + 5, clientY: 60 }),
      );
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(0);
  });

  it("survives a finger tremor smaller than the slop", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown", { clientX: 40, clientY: 60 }));
      document.dispatchEvent(pointerEvent("pointermove", { clientX: 43, clientY: 62 }));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(1);
  });

  it("does not fire when the finger lifts before the threshold", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(LONG_PRESS_MS - 50);
      document.dispatchEvent(pointerEvent("pointerup"));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(0);
  });

  it("leaves a mouse alone, which already has a right button", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown", { pointerType: "mouse" }));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(0);
  });

  it("leaves a text field to the system's own long press", () => {
    const field = document.createElement("textarea");
    const fieldMenus: MouseEvent[] = [];
    field.addEventListener("contextmenu", (event) => fieldMenus.push(event as MouseEvent));
    document.body.append(field);
    renderHook(() => useTouchContextMenu());

    act(() => {
      field.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(fieldMenus).toHaveLength(0);
    field.remove();
  });

  it("treats a second finger as a pinch rather than a press", () => {
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown", { pointerId: 1 }));
      target.dispatchEvent(pointerEvent("pointerdown", { pointerId: 2 }));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(0);
  });

  it("swallows the tap that lifts off the menu it just opened", () => {
    const taps: MouseEvent[] = [];
    target.addEventListener("click", (event) => taps.push(event as MouseEvent));
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(LONG_PRESS_MS);
      document.dispatchEvent(pointerEvent("pointerup"));
      target.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(menus).toHaveLength(1);
    expect(taps).toHaveLength(0);
  });

  it("lets an ordinary tap through", () => {
    const taps: MouseEvent[] = [];
    target.addEventListener("click", (event) => taps.push(event as MouseEvent));
    renderHook(() => useTouchContextMenu());

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(100);
      document.dispatchEvent(pointerEvent("pointerup"));
      target.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(menus).toHaveLength(0);
    expect(taps).toHaveLength(1);
  });

  it("stops listening once the app unmounts", () => {
    const { unmount } = renderHook(() => useTouchContextMenu());
    unmount();

    act(() => {
      target.dispatchEvent(pointerEvent("pointerdown"));
      vi.advanceTimersByTime(LONG_PRESS_MS);
    });

    expect(menus).toHaveLength(0);
  });
});
