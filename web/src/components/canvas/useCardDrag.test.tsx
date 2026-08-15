import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { type Col } from "./geometry";
import { snapBoxesOfColumns, type SnapBox } from "./snapping";
import { useCardDrag } from "./useCardDrag";

const columns: Col[] = [
  {
    node: { id: "a", title: "A" },
    row: 0,
    index: 0,
    width: 140,
    height: 60,
  },
  {
    node: { id: "b", title: "B" },
    row: 0,
    index: 4,
    width: 140,
    height: 60,
  },
];

function event(overrides: Record<string, unknown> = {}) {
  const currentTarget = {
    setPointerCapture: vi.fn(),
    hasPointerCapture: vi.fn(() => true),
    releasePointerCapture: vi.fn(),
  };
  return {
    button: 0,
    pointerId: 1,
    clientX: 0,
    clientY: 0,
    currentTarget,
    ...overrides,
  } as unknown as React.PointerEvent<HTMLElement>;
}

describe("useCardDrag", () => {
  it("commits a clamped multi-step card move", () => {
    const onMove = vi.fn();
    const { result } = renderHook(() =>
      useCardDrag({
        zoom: 1,
        columns,
        selectedIds: new Set(["a"]),
        onSelect: vi.fn(),
        onOpen: vi.fn(),
        onMove,
        onReject: vi.fn(),
      }),
    );

    act(() => {
      result.current.handlers.onPointerDown(event(), "a");
      result.current.handlers.onPointerMove(event({ clientX: 164, clientY: 80 }));
      result.current.handlers.onPointerUp(event({ clientX: 164, clientY: 80 }), "a");
    });

    expect(onMove).toHaveBeenCalledWith(
      {
        a: { x: 1, y: 1 },
        b: { x: 4, y: 0 },
      },
      1,
    );
  });

  it("lands a snapped card on the grid, not where the pointer left it", () => {
    const onMove = vi.fn();
    const { result } = renderHook(() =>
      useCardDrag({
        zoom: 1,
        columns,
        selectedIds: new Set(["a"]),
        onSelect: vi.fn(),
        onOpen: vi.fn(),
        onMove,
        onReject: vi.fn(),
        snapToGrid: true,
      }),
    );

    // Two thirds of a cell across, a third down: the first rounds up, the
    // second rounds back to where it started.
    act(() => {
      result.current.handlers.onPointerDown(event(), "a");
      result.current.handlers.onPointerMove(event({ clientX: 110, clientY: 28 }));
      result.current.handlers.onPointerUp(event({ clientX: 110, clientY: 28 }), "a");
    });

    expect(onMove).toHaveBeenCalledWith(
      {
        a: { x: 1, y: 0 },
        b: { x: 4, y: 0 },
      },
      1,
    );
  });

  it("previews the snapped position rather than the pointer", () => {
    const { result } = renderHook(() =>
      useCardDrag({
        zoom: 1,
        columns,
        selectedIds: new Set(["a"]),
        onSelect: vi.fn(),
        onOpen: vi.fn(),
        onMove: vi.fn(),
        onReject: vi.fn(),
        snapToGrid: true,
      }),
    );

    act(() => {
      result.current.handlers.onPointerDown(event(), "a");
      result.current.handlers.onPointerMove(event({ clientX: 110, clientY: 28 }));
    });

    // A card that previewed at the pointer would jump on release.
    expect(result.current.state?.dx).toBe(164);
    expect(result.current.state?.dy).toBe(0);
  });

  it("suspends snapping while Alt is held", () => {
    const onMove = vi.fn();
    const { result } = renderHook(() =>
      useCardDrag({
        zoom: 1,
        columns,
        selectedIds: new Set(["a"]),
        onSelect: vi.fn(),
        onOpen: vi.fn(),
        onMove,
        onReject: vi.fn(),
        snapToGrid: true,
      }),
    );

    act(() => {
      result.current.handlers.onPointerDown(event(), "a");
      result.current.handlers.onPointerMove(
        event({ clientX: 110, clientY: 28, altKey: true }),
      );
      result.current.handlers.onPointerUp(
        event({ clientX: 110, clientY: 28, altKey: true }),
        "a",
      );
    });

    expect(onMove).toHaveBeenCalledWith(
      {
        a: { x: 0.671, y: 0.35 },
        b: { x: 4, y: 0 },
      },
      1,
    );
  });

  describe("alignment snapping", () => {
    // B sits three rows down, so the two never fight the overlap check.
    const spaced: Col[] = [
      { node: { id: "a", title: "A" }, row: 0, index: 0, width: 140, height: 60 },
      { node: { id: "b", title: "B" }, row: 3, index: 4, width: 140, height: 60 },
    ];
    const snapBoxes = snapBoxesOfColumns(spaced);

    function dragBy(clientX: number, options: Record<string, unknown> = {}) {
      const onMove = vi.fn();
      const { result } = renderHook(() =>
        useCardDrag({
          zoom: 1,
          columns: spaced,
          selectedIds: new Set(["a"]),
          onSelect: vi.fn(),
          onOpen: vi.fn(),
          onMove,
          onReject: vi.fn(),
          snapEnabled: true,
          snapBoxes,
          ...options,
        }),
      );
      act(() => {
        result.current.handlers.onPointerDown(event(), "a");
        result.current.handlers.onPointerMove(event({ clientX, ...options }));
        result.current.handlers.onPointerUp(event({ clientX, ...options }), "a");
      });
      return onMove;
    }

    it("commits the position the card was pulled to, not the raw one", () => {
      // Four pixels short of lining up with B; the magnet closes the gap.
      expect(dragBy(652)).toHaveBeenCalledWith(
        { a: { x: 4, y: 0 }, b: { x: 4, y: 3 } },
        1,
      );
    });

    it("stays free-form while Shift is held", () => {
      expect(dragBy(652, { shiftKey: true })).toHaveBeenCalledWith(
        { a: { x: 3.976, y: 0 }, b: { x: 4, y: 3 } },
        1,
      );
    });

    it("stays free-form when the toggle is off", () => {
      expect(dragBy(652, { snapEnabled: false })).toHaveBeenCalledWith(
        { a: { x: 3.976, y: 0 }, b: { x: 4, y: 3 } },
        1,
      );
    });

    it("reports the line the card landed on while the drag is live", () => {
      const { result } = renderHook(() =>
        useCardDrag({
          zoom: 1,
          columns: spaced,
          selectedIds: new Set(["a"]),
          onSelect: vi.fn(),
          onOpen: vi.fn(),
          onMove: vi.fn(),
          onReject: vi.fn(),
          snapEnabled: true,
          snapBoxes,
        }),
      );
      act(() => {
        result.current.handlers.onPointerDown(event(), "a");
        result.current.handlers.onPointerMove(event({ clientX: 652 }));
      });
      expect(result.current.state?.guides).toEqual([
        expect.objectContaining({ axis: "x", position: 738 }),
      ]);
    });

    it("keeps the pull constant on screen as the board zooms", () => {
      function dragAtDoubleZoom(clientX: number) {
        const onMove = vi.fn();
        const { result } = renderHook(() =>
          useCardDrag({
            zoom: 2,
            columns: spaced,
            selectedIds: new Set(["a"]),
            onSelect: vi.fn(),
            onOpen: vi.fn(),
            onMove,
            onReject: vi.fn(),
            snapEnabled: true,
            snapBoxes,
          }),
        );
        act(() => {
          result.current.handlers.onPointerDown(event(), "a");
          result.current.handlers.onPointerMove(event({ clientX }));
          result.current.handlers.onPointerUp(event({ clientX }), "a");
        });
        return onMove.mock.calls[0][0].a;
      }

      // Two board pixels short is four on screen: still inside the magnet.
      expect(dragAtDoubleZoom(1308)).toEqual({ x: 4, y: 0 });
      // Four board pixels is eight on screen at 2x, so it is now out of reach —
      // the same board gap that does snap at 1x.
      expect(dragAtDoubleZoom(1304)).toEqual({ x: 3.976, y: 0 });
    });

    it("lets the grid round past a line it disagrees with, and drops it", () => {
      // A sticky note centred between two cells: the magnet finds it, and then
      // the lattice rounds the card on past it.
      const offGrid: SnapBox[] = [
        { id: "note", rect: { left: 560, right: 640, top: 250, bottom: 310 } },
      ];
      const onMove = vi.fn();
      const { result } = renderHook(() =>
        useCardDrag({
          zoom: 1,
          columns: spaced,
          selectedIds: new Set(["a"]),
          onSelect: vi.fn(),
          onOpen: vi.fn(),
          onMove,
          onReject: vi.fn(),
          snapEnabled: true,
          snapBoxes: offGrid,
          snapToGrid: true,
        }),
      );

      act(() => {
        result.current.handlers.onPointerDown(event(), "a");
        result.current.handlers.onPointerMove(event({ clientX: 514 }));
      });
      // The line the card was pulled to is no longer the line it is on.
      expect(result.current.state?.guides).toEqual([]);
      expect(result.current.state?.dx).toBe(492);

      act(() => {
        result.current.handlers.onPointerUp(event({ clientX: 514 }), "a");
      });
      expect(onMove).toHaveBeenCalledWith({ a: { x: 3, y: 0 }, b: { x: 4, y: 3 } }, 1);
    });
  });

  it("opens a card when the pointer does not move", () => {
    const onOpen = vi.fn();
    const { result } = renderHook(() =>
      useCardDrag({
        zoom: 1,
        columns,
        selectedIds: new Set(),
        onSelect: vi.fn(),
        onOpen,
        onMove: vi.fn(),
        onReject: vi.fn(),
      }),
    );

    act(() => {
      result.current.handlers.onPointerDown(event(), "a");
      result.current.handlers.onPointerUp(event(), "a");
    });

    expect(onOpen).toHaveBeenCalledWith("a");
  });
});
