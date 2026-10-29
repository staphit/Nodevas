/**
 * The board's scroll is remembered for when the board comes back, but reading
 * it on every scroll event made each pan frame ask the browser for an
 * up-to-date layout: a forced layout of every mounted card whenever anything
 * had been written since the last frame. A pan only marks the view as moved;
 * the position is read once the scrolling settles, or when the view is left.
 */
import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { recallBoardView } from "../canvas/boardViewMemory";
import { useBoardViewport } from "./useBoardViewport";

/** A board with a size, whose scroll position counts its reads. */
function board() {
  const element = document.createElement("div");
  document.body.append(element);
  const scroll = { left: 0, top: 0 };
  const reads = { count: 0 };
  Object.defineProperty(element, "clientWidth", { configurable: true, value: 800 });
  Object.defineProperty(element, "clientHeight", { configurable: true, value: 600 });
  Object.defineProperty(element, "scrollLeft", {
    configurable: true,
    get: () => {
      reads.count++;
      return scroll.left;
    },
    set: (value: number) => {
      scroll.left = value;
    },
  });
  Object.defineProperty(element, "scrollTop", {
    configurable: true,
    get: () => {
      reads.count++;
      return scroll.top;
    },
    set: (value: number) => {
      scroll.top = value;
    },
  });
  /** Where a pan has moved the board to, without reading anything. */
  const panTo = (left: number, top: number) => {
    scroll.left = left;
    scroll.top = top;
    element.dispatchEvent(new Event("scroll"));
  };
  return { element, reads, panTo };
}

function viewportHook(element: HTMLDivElement, project: string) {
  const ref = createRef<HTMLDivElement>();
  (ref as { current: HTMLDivElement }).current = element;
  return renderHook(
    ({ activeProject }: { activeProject: string }) =>
      useBoardViewport({ boardRef: ref, isGraph: true, activeProject, zoom: 1 }),
    { initialProps: { activeProject: project } },
  );
}

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
});

describe("useBoardViewport", () => {
  it("reads no scroll position on a pan's scroll events", () => {
    vi.useFakeTimers();
    const { element, reads, panTo } = board();
    viewportHook(element, "viewport-pan");
    reads.count = 0;

    act(() => {
      for (let step = 1; step <= 30; step++) panTo(1000 + step * 10, 500 + step * 5);
    });

    expect(reads.count).toBe(0);
  });

  it("remembers where a pan stopped once the scrolling settles", () => {
    vi.useFakeTimers();
    const { element, panTo } = board();
    viewportHook(element, "viewport-settle");

    act(() => panTo(1700, 900));
    act(() => vi.advanceTimersByTime(1000));

    expect(recallBoardView("viewport-settle")).toMatchObject({ left: 1700, top: 900 });
  });

  it("remembers where a pan stopped when the board unmounts straight after", () => {
    const { element, panTo } = board();
    const view = viewportHook(element, "viewport-unmount");

    act(() => panTo(1700, 900));
    view.unmount();

    expect(recallBoardView("viewport-unmount")).toMatchObject({ left: 1700, top: 900 });
  });

  it("remembers the last project's pan when the project changes under the board", () => {
    const { element, panTo } = board();
    const view = viewportHook(element, "viewport-first");

    act(() => panTo(1700, 900));
    view.rerender({ activeProject: "viewport-second" });

    expect(recallBoardView("viewport-first")).toMatchObject({ left: 1700, top: 900 });
  });
});
