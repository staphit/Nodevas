import { describe, expect, it } from "vitest";
import {
  EXPLORER_MAX_W,
  EXPLORER_MIN_W,
  explorerMinWidth,
  explorerWidthFor,
} from "./explorerWidth";

// At 200-220px the explorer wrapped its own heading (專案總 / 管), gave project
// names 22-32px and node titles 38px; at 150% text a name was one letter. The
// narrowest it goes is where its rows still read, and larger text needs more.
describe("explorer width", () => {
  it("never goes below where a row still reads", () => {
    expect(EXPLORER_MIN_W).toBeGreaterThanOrEqual(260);
    expect(explorerMinWidth(1)).toBe(EXPLORER_MIN_W);
    expect(explorerMinWidth(0.9)).toBe(EXPLORER_MIN_W);
  });

  it("asks for more room at larger text, within the maximum", () => {
    expect(explorerMinWidth(1.5)).toBeGreaterThanOrEqual(300);
    expect(explorerMinWidth(1.5)).toBeLessThanOrEqual(EXPLORER_MAX_W);
    expect(explorerMinWidth(1.25)).toBeGreaterThan(explorerMinWidth(1));
  });

  it("widens a stored width that is too narrow for the text size, and leaves wider ones", () => {
    expect(explorerWidthFor(260, 1.5)).toBe(explorerMinWidth(1.5));
    expect(explorerWidthFor(400, 1.5)).toBe(400);
    expect(explorerWidthFor(304, 1)).toBe(304);
  });
});
