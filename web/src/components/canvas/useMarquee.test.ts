import { describe, expect, it } from "vitest";
import { wiresInside } from "./useMarquee";

/**
 * jsdom has no SVG geometry, so the wires are stubbed with the two calls the
 * hit test makes: the element's user-space box and its client rect. Keeping
 * them equal means user space is client space, which is what an unzoomed board
 * gives.
 */
function wire(key: string, points: [number, number][]) {
  const xs = points.map(([x]) => x);
  const ys = points.map(([, y]) => y);
  const box = {
    x: Math.min(...xs),
    y: Math.min(...ys),
    width: Math.max(...xs) - Math.min(...xs),
    height: Math.max(...ys) - Math.min(...ys),
  };
  const polyline = {
    getBBox: () => box,
    getBoundingClientRect: () => ({
      left: box.x,
      top: box.y,
      right: box.x + box.width,
      bottom: box.y + box.height,
      width: box.width,
      height: box.height,
    }),
    points: {
      numberOfItems: points.length,
      getItem: (index: number) => ({ x: points[index][0], y: points[index][1] }),
    },
  };
  return {
    dataset: { edgeKey: key },
    querySelector: () => polyline,
  };
}

function container(wires: ReturnType<typeof wire>[]) {
  return { querySelectorAll: () => wires } as unknown as Element;
}

describe("wiresInside", () => {
  it("catches a wire crossing the marquee", () => {
    const board = container([wire("a->b", [[0, 0], [100, 100]])]);
    expect(wiresInside(board, { left: 40, right: 60, top: 40, bottom: 60 })).toEqual([
      "a->b",
    ]);
  });

  it("ignores a diagonal wire that only shares its bounding box", () => {
    const board = container([wire("a->b", [[0, 0], [100, 100]])]);
    // Top-right corner of the box: inside the bounds, far from the line.
    expect(
      wiresInside(board, { left: 80, right: 95, top: 5, bottom: 20 }),
    ).toEqual([]);
  });

  it("catches every wire the marquee crosses", () => {
    const board = container([
      wire("a->b", [[0, 50], [100, 50]]),
      wire("c->d", [[0, 60], [100, 60]]),
      wire("e->f", [[0, 300], [100, 300]]),
    ]);
    expect(
      wiresInside(board, { left: 10, right: 90, top: 40, bottom: 70 }),
    ).toEqual(["a->b", "c->d"]);
  });

  it("follows a wire's bend points", () => {
    const board = container([
      wire("a->b", [[0, 0], [0, 200], [200, 200]]),
    ]);
    // The straight line between the endpoints would miss this rect; the
    // bend goes right through it.
    expect(
      wiresInside(board, { left: 90, right: 110, top: 190, bottom: 210 }),
    ).toEqual(["a->b"]);
  });
});
