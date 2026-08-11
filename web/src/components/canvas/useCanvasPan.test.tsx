import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { MAX_ZOOM } from "./geometry";
import { useCanvasPan } from "./useCanvasPan";

function board() {
  const element = document.createElement("div");
  const inner = document.createElement("div");
  inner.className = "board-inner";
  element.append(inner);
  Object.assign(element, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn(() => true),
    releasePointerCapture: vi.fn(),
    scrollLeft: 0,
    scrollTop: 0,
  });
  Object.defineProperty(element, "clientWidth", { value: 800 });
  Object.defineProperty(element, "clientHeight", { value: 600 });
  Object.defineProperty(element, "getBoundingClientRect", {
    value: () => ({ left: 0, top: 0, width: 800, height: 600, right: 800, bottom: 600 }),
  });
  return element as HTMLDivElement;
}

function event(
  target: HTMLDivElement,
  pointerId: number,
  clientX: number,
  clientY: number,
  pointerType = "touch",
) {
  return {
    pointerId,
    clientX,
    clientY,
    pointerType,
    button: 0,
    currentTarget: target,
    preventDefault: vi.fn(),
  } as unknown as React.PointerEvent<HTMLDivElement>;
}

function panHook(element: HTMLDivElement, displayScale?: number) {
  const ref = createRef<HTMLDivElement>();
  (ref as { current: HTMLDivElement }).current = element;
  return renderHook(() => useCanvasPan({ boardRef: ref, displayScale }));
}

describe("useCanvasPan", () => {
  it("pans the board with one finger", () => {
    const element = board();
    element.scrollLeft = 100;
    element.scrollTop = 50;
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 200, 200));
      result.current.handlers.onPointerMove(event(element, 1, 170, 180));
    });

    expect(element.scrollLeft).toBe(130);
    expect(element.scrollTop).toBe(70);
    expect(result.current.state.panning).toBe(true);
  });

  it("zooms in when two fingers spread apart", () => {
    const element = board();
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 400, 300));
    });
    act(() => {
      // Twice the starting distance, so twice the zoom.
      result.current.handlers.onPointerMove(event(element, 2, 500, 300));
    });

    expect(result.current.state.zoom).toBeCloseTo(2, 5);
  });

  it("zooms out when two fingers close together", () => {
    const element = board();
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 500, 300));
    });
    act(() => {
      result.current.handlers.onPointerMove(event(element, 2, 400, 300));
    });

    expect(result.current.state.zoom).toBeCloseTo(0.5, 5);
  });

  // Recomputing from the starting distance rather than accumulating per move
  // is what makes a pinch reversible instead of drifting.
  it("returns to the starting zoom when the fingers return to where they began", () => {
    const element = board();
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 400, 300));
    });
    act(() => {
      result.current.handlers.onPointerMove(event(element, 2, 460, 300));
    });
    act(() => {
      result.current.handlers.onPointerMove(event(element, 2, 520, 300));
    });
    act(() => {
      result.current.handlers.onPointerMove(event(element, 2, 400, 300));
    });

    expect(result.current.state.zoom).toBeCloseTo(1, 5);
  });

  // The finger moves far, but the distance between the two is unchanged, so
  // nothing should happen: neither a zoom nor the pan that the first finger
  // would have driven on its own.
  it("stops panning when the second finger lands, so a pinch does not also scroll", () => {
    const element = board();
    element.scrollLeft = 100;
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 400, 300));
      result.current.handlers.onPointerMove(event(element, 1, 500, 300));
    });

    expect(element.scrollLeft).toBe(100);
    expect(result.current.state.zoom).toBe(1);
  });

  // Lifting one of two fingers usually means the gesture is over. Continuing
  // as a pan would jerk the board by however far that finger had travelled.
  it("does not turn the remaining finger into a pan when a pinch ends", () => {
    const element = board();
    element.scrollLeft = 100;
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 400, 300));
      result.current.handlers.onPointerUp(event(element, 2, 400, 300));
      result.current.handlers.onPointerMove(event(element, 1, 200, 300));
    });

    expect(element.scrollLeft).toBe(100);
  });

  it("ignores a right mouse button, which belongs to the context menu", () => {
    const element = board();
    const { result } = panHook(element);

    let started = true;
    act(() => {
      started = result.current.handlers.onPointerDown({
        ...event(element, 1, 300, 300, "mouse"),
        button: 2,
      } as unknown as React.PointerEvent<HTMLDivElement>);
    });

    expect(started).toBe(false);
  });

  // The node scale rides the same CSS zoom the user's zoom does, so a pointer
  // lands on a different board coordinate at 150% even though the zoom itself
  // still reads 100%.
  it("converts a pointer through the node scale as well as the zoom", () => {
    const element = board();
    const { result } = panHook(element, 1.5);

    expect(result.current.state.zoom).toBe(1);
    expect(result.current.state.viewScale).toBeCloseTo(1.5, 5);
    expect(result.current.pointFromClient(300, 150)).toEqual({ x: 200, y: 100 });
  });

  it("keeps the middle of the viewport put when the node scale changes", () => {
    const element = board();
    element.scrollLeft = 400;
    element.scrollTop = 300;
    const ref = createRef<HTMLDivElement>();
    (ref as { current: HTMLDivElement }).current = element;
    const { rerender } = renderHook(
      ({ displayScale }: { displayScale: number }) =>
        useCanvasPan({ boardRef: ref, displayScale }),
      { initialProps: { displayScale: 1 } },
    );

    rerender({ displayScale: 2 });

    // The point under the middle of the viewport was board x 800, y 600; at
    // twice the scale it is 1600, 1200, less the half-viewport that puts it
    // back in the middle.
    expect(element.scrollLeft).toBe(1200);
    expect(element.scrollTop).toBe(900);
  });

  it("clamps a pinch that would zoom past the allowed range", () => {
    const element = board();
    const { result } = panHook(element);

    act(() => {
      result.current.handlers.onPointerDown(event(element, 1, 300, 300));
      result.current.handlers.onPointerDown(event(element, 2, 330, 300));
    });
    act(() => {
      result.current.handlers.onPointerMove(event(element, 2, 3000, 300));
    });

    expect(result.current.state.zoom).toBe(MAX_ZOOM);
  });
});
