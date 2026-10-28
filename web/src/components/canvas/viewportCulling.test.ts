import { describe, expect, it } from "vitest";
import { viewWindowFor } from "./viewportCulling";

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
