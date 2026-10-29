import { act, fireEvent, render } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { GraphMinimap } from "./GraphMinimap";
import type { Col } from "./geometry";

const cols: Col[] = ["a", "b", "c"].map((id, index) => ({
  node: { id, title: id },
  row: 0,
  index,
  width: 152,
  height: 68,
}));

function props(overrides: Record<string, unknown> = {}) {
  return {
    cols,
    selectedGraphNodeIDs: new Set<string>(),
    boardRef: createRef<HTMLDivElement>(),
    boardW: 4000,
    svgH: 2000,
    zoom: 1,
    boardViewport: { width: 1000, height: 600 },
    graphOffsetX: 1640,
    graphOffsetY: 720,
    centerX: vi.fn((column: Col) => 1640 + column.index * 164 + 82),
    rowTop: vi.fn((row: number) => 720 + row * 80),
    ...overrides,
  };
}

describe("GraphMinimap", () => {
  // The board hands down new layout functions on every render; the map's
  // dots only move when the layout they read does.
  it("does not lay its dots out again while the layout is unchanged", () => {
    const first = props();
    const { container, rerender } = render(<GraphMinimap {...first} />);
    expect(container.querySelectorAll(".graph-minimap-node")).toHaveLength(3);

    const same = props({ selectedGraphNodeIDs: first.selectedGraphNodeIDs });
    rerender(<GraphMinimap {...same} />);
    expect(same.centerX).not.toHaveBeenCalled();

    const moved = props({ selectedGraphNodeIDs: first.selectedGraphNodeIDs, graphOffsetX: 1700 });
    rerender(<GraphMinimap {...moved} />);
    expect(moved.centerX).toHaveBeenCalled();
  });

  // 170x108 is a corner of a full-width board, but a third of the graph pane
  // when the timeline is open below it (about 315px tall) or the explorer is
  // wide: there it covered the cards it was meant to help find.
  it("shrinks in a short or narrow pane", () => {
    const size = (width: number, height: number) => {
      const { container, unmount } = render(
        <GraphMinimap {...props({ boardViewport: { width, height } })} />,
      );
      const map = container.querySelector<HTMLElement>(".graph-minimap")!;
      const result = { width: parseFloat(map.style.width), height: parseFloat(map.style.height) };
      unmount();
      return result;
    };
    expect(size(1265, 700)).toEqual({ width: 170, height: 108 });
    const short = size(773, 315);
    expect(short.height).toBeLessThanOrEqual(315 / 4);
    expect(short.width / short.height).toBeCloseTo(170 / 108, 1);
    const narrow = size(773, 665);
    expect(narrow.width).toBeLessThan(170);
    // Never so small that it stops being a map.
    expect(size(300, 200).width).toBeGreaterThanOrEqual(100);
  });

  // The frame follows every pan frame. Placed by left/top it dirtied the map's
  // layout, and the next scroll read (its own, then the board's culling) laid
  // out the whole document again with every dot on it: two forced layouts of
  // ~2.7ms each per pan frame on a 2000-card board. A transform moves it
  // without a layout, and the scroll position is read before anything is
  // written, so nothing in the frame is forced.
  it("follows a pan with a transform, never a layout property", () => {
    const boardRef = createRef<HTMLDivElement>();
    const board = document.createElement("div");
    document.body.appendChild(board);
    (boardRef as { current: HTMLDivElement | null }).current = board;
    const frames: FrameRequestCallback[] = [];
    const raf = vi
      .spyOn(window, "requestAnimationFrame")
      .mockImplementation((callback) => frames.push(callback));
    try {
      const { container } = render(<GraphMinimap {...props({ boardRef, zoom: 2 })} />);
      const frame = container.querySelector<HTMLElement>(".graph-minimap-viewport")!;
      // The frame keeps the viewport's size; only where it sits moves.
      expect(frame.style.width).toBe("12.5%");
      expect(frame.style.height).toBe("15%");

      act(() => {
        board.scrollLeft = 800;
        board.scrollTop = 400;
        fireEvent.scroll(board);
      });
      act(() => frames.splice(0).forEach((callback) => callback(0)));

      const mover = frame.closest<HTMLElement>(".graph-minimap-track")!;
      expect(mover).not.toBeNull();
      // 800 / 2 / 4000 and 400 / 2 / 2000 of the map, in its own size.
      expect(mover.style.transform).toBe("translate(10%, 10%)");
      for (const element of [mover, frame]) {
        expect(element.style.left).toBe("");
        expect(element.style.top).toBe("");
      }
    } finally {
      raf.mockRestore();
      board.remove();
    }
  });
});
