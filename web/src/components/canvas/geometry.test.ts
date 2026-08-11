import { describe, expect, it } from "vitest";
import {
  CARD_MAX_H,
  CARD_MAX_W,
  CARD_MIN_H,
  CARD_MIN_W,
  addLocalDays,
  centerX,
  clampZoom,
  contentPointFromClient,
  dayKey,
  edgeKeyEndpoints,
  parseLocalDay,
  resizeCardByKeyboard,
  resizeCardSize,
  rowTop,
  type Col,
} from "./geometry";

const column: Col = {
  node: { id: "node-a", title: "A" },
  row: 2,
  index: 3,
  width: 140,
  height: 60,
};

describe("canvas geometry", () => {
  it("formats and parses local calendar days without timezone shifts", () => {
    const date = parseLocalDay("2026-07-31");
    expect(date).not.toBeNull();
    expect(dayKey(date!)).toBe("2026-07-31");
    expect(dayKey(addLocalDays(date!, 1))).toBe("2026-08-01");
    expect(parseLocalDay("2026-02-30")).toBeNull();
    expect(parseLocalDay("not-a-day")).toBeNull();
  });

  it("parses only ordinary dependency wire keys", () => {
    expect(edgeKeyEndpoints("source->target")).toEqual({ from: "source", to: "target" });
    expect(edgeKeyEndpoints("gate:gate-1")).toBeNull();
    expect(edgeKeyEndpoints("source")).toBeNull();
    expect(edgeKeyEndpoints("->target")).toBeNull();
  });

  it("clamps card resizing at the shared layout bounds", () => {
    expect(resizeCardSize(180, 90, 1, 1, 1000, 1000, 1)).toEqual({
      width: CARD_MAX_W,
      height: CARD_MAX_H,
    });
    expect(resizeCardSize(180, 90, -1, -1, 1000, 1000, 1)).toEqual({
      width: CARD_MIN_W,
      height: CARD_MIN_H,
    });
    expect(resizeCardByKeyboard(CARD_MIN_W, CARD_MIN_H, "ArrowLeft")).toEqual({
      width: CARD_MIN_W,
      height: CARD_MIN_H,
    });
    expect(resizeCardByKeyboard(CARD_MAX_W, CARD_MAX_H, "ArrowRight")).toEqual({
      width: CARD_MAX_W,
      height: CARD_MAX_H,
    });
    expect(resizeCardByKeyboard(180, 90, "Escape")).toBeNull();
  });

  it("maps graph and timeline columns through one coordinate function", () => {
    expect(
      centerX(column, {
        isGraph: true,
        graphOffsetX: 20,
        graphOffsetY: 40,
        timelineCellWidth: 220,
        dayY: [],
        dayCount: 0,
      }),
    ).toBe(20 + 3 * 164 + 82);
    expect(
      rowTop(2, {
        isGraph: false,
        graphOffsetX: 0,
        graphOffsetY: 0,
        timelineCellWidth: 220,
        dayY: [10, 90, 180],
        dayCount: 3,
      }),
    ).toBe(180);
  });

  it("keeps zoom and pointer conversion bounded and deterministic", () => {
    expect(clampZoom(0.01)).toBe(0.5);
    expect(clampZoom(1.04)).toBe(1);
    expect(clampZoom(4)).toBe(2);
    expect(
      contentPointFromClient(140, 90, { left: 40, top: 30 }, 2),
    ).toEqual({ x: 50, y: 30 });
  });
});
