import { describe, expect, it } from "vitest";
import {
  constrainPointOutsideRects,
  nearestClearRatio,
  type Point,
  type Rect,
} from "./geometry";
import {
  constrainPointOutsideIndex,
  nearestClearRatioInIndex,
  obstacleIndex,
} from "./obstacleIndex";

/** A small seeded generator, so a failure replays. */
function random(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 2 ** 32;
  };
}

function board(seed: number, count: number, spread: number, maxSize: number): Rect[] {
  const next = random(seed);
  return Array.from({ length: count }, () => {
    const left = Math.round(next() * spread);
    const top = Math.round(next() * spread);
    return {
      left,
      top,
      right: left + 8 + Math.round(next() * maxSize),
      bottom: top + 8 + Math.round(next() * maxSize),
    };
  });
}

const intersects = (a: Rect, b: Rect) =>
  a.left <= b.right && a.right >= b.left && a.top <= b.bottom && a.bottom >= b.top;

describe("obstacleIndex", () => {
  it("finds the obstacles meeting a box, in board order", () => {
    const rects = board(1, 600, 4000, 400);
    const index = obstacleIndex(rects);
    const next = random(2);
    for (let i = 0; i < 200; i++) {
      const left = next() * 4400 - 200;
      const top = next() * 4400 - 200;
      const query = { left, top, right: left + next() * 900, bottom: top + next() * 900 };
      expect(index.near(query)).toEqual(rects.filter((rect) => intersects(rect, query)));
    }
  });

  it("finds an obstacle by its edge, and nothing on an empty board", () => {
    const rect = { left: 100, top: 100, right: 200, bottom: 150 };
    const index = obstacleIndex([rect]);
    expect(index.near({ left: 200, top: 150, right: 260, bottom: 160 })).toEqual([rect]);
    expect(index.near({ left: 201, top: 0, right: 260, bottom: 99 })).toEqual([]);
    expect(obstacleIndex([]).near({ left: 0, top: 0, right: 1e6, bottom: 1e6 })).toEqual([]);
  });
});

describe("routing against an index", () => {
  // The index only skips obstacles that cannot change the answer, so routing
  // through it is the same as routing against every card on the board — on
  // a sparse board and on a crowded one where a box is pushed from card to
  // card well away from where it started.
  for (const [name, seed, count, spread, maxSize] of [
    ["a sparse board", 3, 400, 6000, 200],
    ["a crowded board", 4, 400, 1500, 300],
  ] as const) {
    it(`pushes a point clear the same way on ${name}`, () => {
      const rects = board(seed, count, spread, maxSize);
      const index = obstacleIndex(rects);
      const next = random(seed + 10);
      for (let i = 0; i < 300; i++) {
        const point: Point = { x: next() * spread, y: next() * spread };
        const clearance = Math.round(next() * 50);
        expect(constrainPointOutsideIndex(point, index, clearance)).toEqual(
          constrainPointOutsideRects(point, rects, clearance),
        );
      }
    });

    it(`finds the same clear spot along a wire on ${name}`, () => {
      const rects = board(seed, count, spread, maxSize);
      const index = obstacleIndex(rects);
      const next = random(seed + 20);
      for (let i = 0; i < 300; i++) {
        const start: Point = { x: next() * spread, y: next() * spread };
        const end: Point =
          i % 5 === 0
            ? { x: start.x, y: start.y + next() * 600 }
            : { x: start.x + next() * 800 - 400, y: start.y + next() * 800 - 400 };
        const preferred = next();
        expect(nearestClearRatioInIndex(start, end, preferred, index, 44, 18)).toBe(
          nearestClearRatio(start, end, preferred, rects, 44, 18),
        );
      }
    });
  }
});
