import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  CARD_MAX_H,
  CARD_MAX_W,
  CARD_MIN_H,
  CARD_MIN_W,
} from "./geometry";
import { useCardResize } from "./useCardResize";

function pointerEvent(overrides: Record<string, unknown> = {}) {
  const target = {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn(() => true),
    releasePointerCapture: vi.fn(),
  };
  return {
    button: 0,
    pointerId: 1,
    clientX: 0,
    clientY: 0,
    currentTarget: target,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
    ...overrides,
  } as unknown as React.PointerEvent<HTMLElement>;
}

describe("useCardResize", () => {
  it("owns pointer state and commits clamped dimensions", () => {
    const onCommit = vi.fn();
    const { result } = renderHook(() => useCardResize({ zoom: 1, onCommit }));

    act(() => {
      result.current.handlers.onPointerDown(pointerEvent(), "node-a", 1, 1, 180, 90);
    });
    expect(result.current.state?.nodeId).toBe("node-a");

    act(() => {
      result.current.handlers.onPointerMove(
        pointerEvent({ clientX: 1000, clientY: 1000 }),
      );
    });
    expect(result.current.state?.width).toBe(CARD_MAX_W);
    expect(result.current.state?.height).toBe(CARD_MAX_H);

    act(() => {
      result.current.handlers.onPointerUp(
        pointerEvent({ clientX: 1000, clientY: 1000 }),
      );
    });
    expect(onCommit).toHaveBeenCalledWith("node-a", {
      width: CARD_MAX_W,
      height: CARD_MAX_H,
    });
    expect(result.current.state).toBeNull();
  });

  describe("alignment snapping", () => {
    // The card is 180 wide around x = 300, so its right edge starts at 390.
    const rectOf = () => ({ left: 210, right: 390, top: 0, bottom: 90 });
    // A neighbour's left edge four pixels past where a small pull would land.
    const snapBoxes = [
      { id: "node-b", rect: { left: 400, right: 500, top: 900, bottom: 950 } },
    ];

    function resizeBy(dx: number, options: Record<string, unknown> = {}) {
      const onCommit = vi.fn();
      const { result } = renderHook(() =>
        useCardResize({
          zoom: 1,
          onCommit,
          snapEnabled: true,
          snapBoxes,
          rectOf,
          ...options,
        }),
      );
      act(() => {
        result.current.handlers.onPointerDown(pointerEvent(), "node-a", 1, 0, 180, 90);
        result.current.handlers.onPointerMove(pointerEvent({ clientX: dx, ...options }));
        result.current.handlers.onPointerUp(pointerEvent({ clientX: dx, ...options }));
      });
      return onCommit;
    }

    it("grows by twice the pull, because the card is pinned at its centre", () => {
      // A 5px drag widens the card by 10 (both edges move), putting the right
      // edge at 395 — five short of the neighbour, so the magnet adds 10 more.
      expect(resizeBy(5)).toHaveBeenCalledWith("node-a", { width: 200, height: 90 });
    });

    it("stays free-form while Shift is held", () => {
      expect(resizeBy(5, { shiftKey: true })).toHaveBeenCalledWith("node-a", {
        width: 190,
        height: 90,
      });
    });

    it("stays free-form when the toggle is off", () => {
      expect(resizeBy(5, { snapEnabled: false })).toHaveBeenCalledWith("node-a", {
        width: 190,
        height: 90,
      });
    });
  });

  it("uses keyboard steps while respecting minimum bounds", () => {
    const onCommit = vi.fn();
    const { result } = renderHook(() => useCardResize({ zoom: 1, onCommit }));

    act(() => {
      result.current.handlers.onKeyDown(
        {
          key: "ArrowLeft",
          shiftKey: true,
          preventDefault: vi.fn(),
          stopPropagation: vi.fn(),
        } as unknown as React.KeyboardEvent<HTMLElement>,
        "node-a",
        -1,
        0,
        CARD_MIN_W,
        CARD_MIN_H,
      );
    });
    expect(onCommit).toHaveBeenCalledWith("node-a", {
      width: CARD_MIN_W,
      height: CARD_MIN_H,
    });
  });
});
