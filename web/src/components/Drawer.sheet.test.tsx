import { render } from "@testing-library/react";
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { DrawerSheetHandle } from "./Drawer";

// jsdom has no PointerEvent, and the handle only reads the pointer fields, so a
// MouseEvent carrying them is the same shape as far as React is concerned.
function pointerEvent(type: string, clientY: number) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientY,
  });
  Object.defineProperty(event, "pointerType", { value: "touch" });
  Object.defineProperty(event, "pointerId", { value: 1 });
  return event;
}

const SHEET_VH = 76;
/** What the drag distances below are measured against. */
const SHEET_PX = (SHEET_VH / 100) * window.innerHeight;

function renderHandle() {
  const onHeightChange = vi.fn();
  const onOffsetChange = vi.fn();
  const onDragStateChange = vi.fn();
  const onDismiss = vi.fn();
  const { container } = render(
    <DrawerSheetHandle
      heightVh={SHEET_VH}
      onHeightChange={onHeightChange}
      onOffsetChange={onOffsetChange}
      onDragStateChange={onDragStateChange}
      onDismiss={onDismiss}
    />,
  );
  const handle = container.querySelector<HTMLDivElement>(".drawer-sheet-handle");
  if (!handle) throw new Error("找不到底部面板的拖曳把手");
  return { handle, onHeightChange, onOffsetChange, onDragStateChange, onDismiss };
}

function dragBy(handle: HTMLDivElement, from: number, to: number) {
  act(() => {
    handle.dispatchEvent(pointerEvent("pointerdown", from));
    handle.dispatchEvent(pointerEvent("pointermove", to));
    handle.dispatchEvent(pointerEvent("pointerup", to));
  });
}

describe("DrawerSheetHandle", () => {
  it("draws a grab bar and keeps it out of the accessibility tree", () => {
    const { handle } = renderHandle();
    expect(handle).toHaveAttribute("aria-hidden", "true");
    expect(handle.querySelector(".drawer-sheet-grip")).not.toBeNull();
  });

  it("closes the sheet when dragged down past a third of its height", () => {
    const { handle, onDismiss, onOffsetChange, onDragStateChange } = renderHandle();
    const distance = Math.round(SHEET_PX * 0.5);

    dragBy(handle, 0, distance);

    expect(onOffsetChange).toHaveBeenCalledWith(distance);
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(onDragStateChange).toHaveBeenLastCalledWith(false);
  });

  it("settles back when the drag stops short of the threshold", () => {
    const { handle, onDismiss, onOffsetChange } = renderHandle();

    dragBy(handle, 0, 40);

    expect(onDismiss).not.toHaveBeenCalled();
    expect(onOffsetChange).toHaveBeenLastCalledWith(0);
  });

  it("grows the sheet when dragged upwards instead of dismissing it", () => {
    const { handle, onHeightChange, onDismiss } = renderHandle();

    dragBy(handle, 400, 300);

    expect(onHeightChange).toHaveBeenCalledTimes(1);
    expect(onHeightChange.mock.calls[0][0]).toBeGreaterThan(SHEET_VH);
    expect(onDismiss).not.toHaveBeenCalled();
  });
});
