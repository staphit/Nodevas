import { afterEach, describe, expect, it } from "vitest";
import { VIEWPORT_MARGIN, clampToViewport } from "./floatingPanel";

const originalWidth = window.innerWidth;
const originalHeight = window.innerHeight;

function viewport(width: number, height: number) {
  Object.defineProperty(window, "innerWidth", { value: width, configurable: true });
  Object.defineProperty(window, "innerHeight", { value: height, configurable: true });
}

afterEach(() => viewport(originalWidth, originalHeight));

describe("clampToViewport", () => {
  it("leaves a panel that already fits where it was asked for", () => {
    viewport(1000, 800);
    expect(clampToViewport({ left: 100, top: 200 }, 300, 400)).toEqual({
      left: 100,
      top: 200,
    });
  });

  it("pulls a panel back from the far edges by its measured size", () => {
    viewport(1000, 800);
    // The old code reserved a hardcoded size and overhung whenever the guess
    // was too small; 380 wide at x=900 has to come back to 1000-380-8.
    expect(clampToViewport({ left: 900, top: 700 }, 380, 620)).toEqual({
      left: 1000 - 380 - VIEWPORT_MARGIN,
      top: 800 - 620 - VIEWPORT_MARGIN,
    });
  });

  it("keeps the near edge of a panel too big to fit", () => {
    viewport(400, 300);
    // No placement fits, and the near corner is the one worth keeping: that is
    // where the heading is, and the panel scrolls inside its own max-height.
    expect(clampToViewport({ left: 200, top: 200 }, 600, 900)).toEqual({
      left: VIEWPORT_MARGIN,
      top: VIEWPORT_MARGIN,
    });
  });

  it("pushes a panel anchored off the top-left back into view", () => {
    viewport(1000, 800);
    expect(clampToViewport({ left: -50, top: -20 }, 300, 400)).toEqual({
      left: VIEWPORT_MARGIN,
      top: VIEWPORT_MARGIN,
    });
  });
});
