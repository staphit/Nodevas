import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useMarquee } from "./useMarquee";

function event(board: HTMLDivElement, overrides: Record<string, unknown> = {}) {
  return {
    pointerId: 1,
    clientX: 10,
    clientY: 10,
    ctrlKey: true,
    metaKey: false,
    currentTarget: board,
    preventDefault: vi.fn(),
    ...overrides,
  } as unknown as React.PointerEvent<HTMLDivElement>;
}

function boardWithCards(count: number) {
  const board = document.createElement("div");
  Object.assign(board, {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn(() => true),
    releasePointerCapture: vi.fn(),
  });
  for (let index = 0; index < count; index++) {
    const card = document.createElement("div");
    card.className = "col-card";
    card.dataset.nodeId = "node-" + (index + 1);
    Object.defineProperty(card, "getBoundingClientRect", {
      value: () => ({
        left: 20 + index * 40,
        right: 50 + index * 40,
        top: 20,
        bottom: 60,
      }),
    });
    board.append(card);
  }
  return board;
}

describe("useMarquee", () => {
  it("selects the single card hit by the marquee", () => {
    const single = vi.fn();
    const multi = vi.fn();
    const { result } = renderHook(() =>
      useMarquee({
        enabled: true,
        pointFromClient: (x, y) => ({ x, y }),
        onSingleSelect: single,
        onMultiSelect: multi,
        onClearSelection: vi.fn(),
      }),
    );
    const board = boardWithCards(1);

    act(() => {
      result.current.handlers.onPointerDown(event(board));
      result.current.handlers.onPointerMove(event(board, { clientX: 60, clientY: 70 }));
      result.current.handlers.onPointerUp(event(board, { clientX: 60, clientY: 70 }));
    });

    expect(single).toHaveBeenCalledWith("node-1");
    expect(multi).not.toHaveBeenCalled();
    expect(result.current.state).toBeNull();
  });

  // A finger cannot hold Ctrl, so the board's marquee mode says the same thing
  // for the whole board at once.
  it("ignores a plain drag while the modifier is what starts a marquee", () => {
    const multi = vi.fn();
    const { result } = renderHook(() =>
      useMarquee({
        enabled: true,
        pointFromClient: (x, y) => ({ x, y }),
        onSingleSelect: vi.fn(),
        onMultiSelect: multi,
        onClearSelection: vi.fn(),
      }),
    );
    const board = boardWithCards(1);

    let started = true;
    act(() => {
      started = result.current.handlers.onPointerDown(event(board, { ctrlKey: false }));
    });

    expect(started).toBe(false);
    expect(multi).not.toHaveBeenCalled();
  });

  it("starts a marquee without a modifier once the board is in marquee mode", () => {
    const single = vi.fn();
    const { result } = renderHook(() =>
      useMarquee({
        enabled: true,
        withoutModifier: true,
        pointFromClient: (x, y) => ({ x, y }),
        onSingleSelect: single,
        onMultiSelect: vi.fn(),
        onClearSelection: vi.fn(),
      }),
    );
    const board = boardWithCards(1);

    act(() => {
      result.current.handlers.onPointerDown(event(board, { ctrlKey: false }));
      result.current.handlers.onPointerUp(event(board, { clientX: 60, clientY: 70 }));
    });

    expect(single).toHaveBeenCalledWith("node-1");
  });

  it("returns all intersecting cards for a multi-selection", () => {
    const multi = vi.fn();
    const { result } = renderHook(() =>
      useMarquee({
        enabled: true,
        pointFromClient: (x, y) => ({ x, y }),
        onSingleSelect: vi.fn(),
        onMultiSelect: multi,
        onClearSelection: vi.fn(),
      }),
    );
    const board = boardWithCards(2);

    act(() => {
      result.current.handlers.onPointerDown(event(board));
      result.current.handlers.onPointerUp(event(board, { clientX: 120, clientY: 70 }));
    });

    expect(multi).toHaveBeenCalledWith(["node-1", "node-2"]);
  });
});
