import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import {
  EXPLORER_DEFAULT_W,
  EXPLORER_MAX_W,
  EXPLORER_MIN_W,
  EXPLORER_RAIL_W,
  explorerMinWidth,
  explorerRailWidth,
  explorerShellStyle,
  explorerWidthFor,
} from "./explorerWidth";

// At 200-220px the explorer wrapped its own heading (專案總 / 管), gave project
// names 22-32px and node titles 38px; at 150% text a name was one letter. The
// narrowest it goes is where its rows still read, and larger text needs more.
//
// The width dragged is the whole shell, tab rail included; the rows live in
// the panel beside the rail. Counting the rail into the minimum left the panel
// 207px at "260": a short project name was cut and its current-project badge
// wrapped. The minimum is the panel's, with the rail added on top.
describe("explorer width", () => {
  it("never goes below where a row still reads", () => {
    expect(EXPLORER_MIN_W).toBeGreaterThanOrEqual(260);
    expect(explorerMinWidth(1)).toBe(EXPLORER_MIN_W + EXPLORER_RAIL_W);
    expect(explorerMinWidth(0.9)).toBe(EXPLORER_MIN_W + EXPLORER_RAIL_W);
  });

  // The rail's tabs carry a label under the icon. At 150% text "Nodes" was
  // 44px on a 40px tab and "Agent" filled it edge to edge, so the active
  // tab's outline ran through the "A". The tabs, and the rail with them,
  // widen with the text size (never below their width at 100%).
  it("counts the tab rail as the stylesheet draws it, at every text size", () => {
    const css = readFileSync(join(__dirname, "explorer", "explorer.css"), "utf8");
    const rail =
      /\.ex-rail \{[^}]*flex: 0 0 calc\((\d+)px \+ (\d+)px \* max\(1, var\(--font-scale, 1\)\)\);[^}]*border-right: (\d+)px/.exec(
        css,
      );
    expect(rail).not.toBeNull();
    const [, padding, tab, border] = rail!.map(Number);
    const tabRule = /\.ex-rail button \{([^}]*)\}/.exec(css);
    expect(tabRule?.[1]).toMatch(new RegExp(String.raw`width: calc\(${tab}px \* max\(1, var\(--font-scale, 1\)\)\)`));
    for (const scale of [0.9, 1, 1.25, 1.5]) {
      expect(explorerRailWidth(scale)).toBe(Math.round(padding + tab * Math.max(1, scale)) + border);
    }
    expect(EXPLORER_RAIL_W).toBe(explorerRailWidth(1));
  });

  it("keeps the panel's minimum beside a rail widened for larger text", () => {
    expect(explorerMinWidth(1.5) - explorerRailWidth(1.5)).toBeGreaterThanOrEqual(300);
  });

  it("starts wide enough for the panel's minimum", () => {
    expect(EXPLORER_DEFAULT_W).toBeGreaterThanOrEqual(explorerMinWidth(1));
  });

  it("asks for more room at larger text, within the maximum", () => {
    expect(explorerMinWidth(1.5) - EXPLORER_RAIL_W).toBeGreaterThanOrEqual(300);
    expect(explorerMinWidth(1.5)).toBeLessThanOrEqual(EXPLORER_MAX_W);
    expect(explorerMinWidth(1.25)).toBeGreaterThan(explorerMinWidth(1));
  });

  it("widens a stored width that is too narrow for the text size, and leaves wider ones", () => {
    expect(explorerWidthFor(260, 1.5)).toBe(explorerMinWidth(1.5));
    expect(explorerWidthFor(400, 1.5)).toBe(400);
    expect(explorerWidthFor(400, 1)).toBe(400);
    expect(explorerWidthFor(304, 1)).toBe(explorerMinWidth(1));
  });
});

// On a phone-width screen the explorer is an overlay whose width the
// stylesheet sets (min(86vw, 304px)). The width dragged on a desktop used to
// be written inline anyway, which beats the stylesheet: a 520px panel on a
// 500px screen cut off its own collapse button and left no backdrop to tap.
describe("explorer shell style", () => {
  it("sets the dragged width beside the board", () => {
    expect(explorerShellStyle({ width: 520, collapsed: false, narrow: false })).toEqual({
      width: 520,
      flexBasis: 520,
    });
  });

  it("leaves the overlay's width to the stylesheet on a narrow screen", () => {
    expect(explorerShellStyle({ width: 520, collapsed: false, narrow: true })).toBeUndefined();
  });

  it("sets nothing while collapsed", () => {
    expect(explorerShellStyle({ width: 520, collapsed: true, narrow: false })).toBeUndefined();
  });
});

it("is what the app shell draws the explorer with", () => {
  const app = readFileSync(join(__dirname, "App.tsx"), "utf8");
  expect(app).toMatch(/style=\{explorerShellStyle\(\{[^}]*narrow/);
});

// The end-to-end specs read the explorer's width off its separator. When the
// default moved from 304 to 320 they kept the old number, so a double click
// "reset" and the settings reset both failed against a correct app. Every
// width they expect as a literal is the default one.
it("is the default width the end-to-end specs expect", () => {
  const dir = join(__dirname, "..", "e2e");
  const literals = readdirSync(dir)
    .filter((name) => name.endsWith(".spec.ts"))
    .flatMap((name) =>
      [...readFileSync(join(dir, name), "utf8").matchAll(/"aria-valuenow",\s*"(\d+)"/g)].map(
        (match) => `${name}: ${match[1]}`,
      ),
    );
  expect(literals.length).toBeGreaterThan(0);
  for (const literal of literals) expect(literal).toMatch(new RegExp(`: ${EXPLORER_DEFAULT_W}$`));
});
