import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet. The
// Problems panel takes 320px beside the board, and the minimap used to step
// left of it, onto the narrower board: cards the opening had framed clear of
// the map's corner were under it once Problems was open (Daily work schedule,
// 1280x800, explorer 320: the map over a card's whole face). While Problems is
// open the map rides over the panel's foot instead, above the panel, and the
// panel keeps room at its end so nothing in it is left under the map.
const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s+");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

function px(value: string | undefined): number {
  return value === undefined ? Number.NaN : parseFloat(value);
}

describe("the minimap with the Problems panel open", () => {
  const panel = rule(".problems-panel");
  const panelWidth = px(/--problems-w:\s*([\d.]+)px/.exec(panel)?.[1]);
  const map = rule(".lane-body.problems-open .graph-minimap");

  it("sits over the panel, never over the board beside it", () => {
    const right = px(/(?:^|;|\s)right:\s*([\d.]+)px/.exec(map)?.[1]);
    // The map is 170px wide at most: its left edge stays inside the panel.
    expect(right + 170).toBeLessThanOrEqual(panelWidth);
  });

  it("leaves the panel's last rows clear of it", () => {
    const open = rule(".lane-body.problems-open .problems-panel");
    const bottom = /padding-bottom:\s*([^;]+);/.exec(open)?.[1] ?? "";
    // At least the map's full height above the Copilot button's lift.
    expect(bottom).toMatch(/108px/);
  });
});
