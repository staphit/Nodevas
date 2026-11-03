/**
 * The culled window follows the board's scroll. Its size is the board's
 * measured size, handed in by useBoardViewport (a ResizeObserver), so a pan
 * frame reads only the scroll position — never clientWidth/clientHeight, which
 * after a frame's style writes is a forced layout of every mounted card.
 */
import { act, renderHook } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { manualFrames } from "./boardTestKit";
import { useViewWindow } from "./useViewWindow";
import { viewWindowFor } from "./viewportCulling";

function board() {
  const element = document.createElement("div");
  document.body.append(element);
  const sizeReads = { count: 0 };
  const size = (value: number) => ({
    configurable: true,
    get: () => {
      sizeReads.count++;
      return value;
    },
  });
  Object.defineProperty(element, "clientWidth", size(1000));
  Object.defineProperty(element, "clientHeight", size(600));
  return { element, sizeReads };
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("useViewWindow", () => {
  it("follows a pan without reading the board's size", () => {
    const frames = manualFrames();
    const { element, sizeReads } = board();
    const ref = createRef<HTMLDivElement>();
    (ref as { current: HTMLDivElement }).current = element;
    const viewport = { width: 1000, height: 600 };
    const { result } = renderHook(() =>
      useViewWindow({ boardRef: ref, enabled: true, zoom: 0.5, viewport }),
    );
    sizeReads.count = 0;

    for (let step = 1; step <= 12; step++) {
      act(() => {
        element.scrollLeft = step * 400;
        element.scrollTop = step * 250;
        element.dispatchEvent(new Event("scroll"));
      });
      act(() => frames.flush());
    }

    expect(sizeReads.count).toBe(0);
    expect(result.current).toEqual(
      viewWindowFor({ left: 12 * 400, top: 12 * 250 }, viewport, 0.5),
    );
  });
});
