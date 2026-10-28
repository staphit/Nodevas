import { describe, expect, it } from "vitest";
import { obstaclesNear, viewWindowFor } from "./viewportCulling";

const box = (left: number, top: number, size = 10) => ({
  left,
  top,
  right: left + size,
  bottom: top + size,
});

describe("viewWindowFor", () => {
  it("snaps the visible board plus its margin outward", () => {
    expect(viewWindowFor({ left: 1312, top: 640 }, { width: 1000, height: 600 }, 1)).toEqual({
      left: 960,
      top: 320,
      right: 2880,
      bottom: 1600,
    });
  });

  it("is null for a board with no size", () => {
    expect(viewWindowFor({ left: 0, top: 0 }, { width: 0, height: 600 }, 1)).toBeNull();
  });
});

describe("obstaclesNear", () => {
  // A wire routes against the cards around its own ends and the cards in
  // view, where a pointer can move its gate or bends; on a culled board the
  // rest of the board cannot change it and is not worth testing against.
  it("keeps the obstacles near the wire's box or in view, in board order", () => {
    const obstacles = [
      box(0, 0), // in view
      box(5000, 5000), // far from both
      box(1050, 0), // within the pad of the wire's box
      box(1000, 0), // inside the wire's box
      box(3000, 0), // past the pad
    ];
    const near = obstaclesNear(
      obstacles,
      { left: 900, top: 0, right: 1010, bottom: 10 },
      { left: 0, top: 0, right: 100, bottom: 100 },
      100,
    );
    expect(near).toEqual([obstacles[0], obstacles[2], obstacles[3]]);
  });
});
