import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet. The
// Problems panel takes 320px beside the board. The minimap used to step left
// of it, onto the narrower board, and covered cards the opening had framed
// clear of its corner; moved over the panel's foot instead, it covered the
// panel's own rows whenever the pane was short (1280x800 at 150%, timeline
// open: a 330px graph pane, the map over the second problem). While Problems
// is open there is no corner free for it, so it is not shown — closing the
// panel brings it back — and the panel keeps room at its end only for the
// Copilot button.
const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s+");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

describe("the minimap with the Problems panel open", () => {
  it("is not shown, over the board or over the panel", () => {
    const map = rule(".lane-body.problems-open .graph-minimap");
    expect(map).toMatch(/display:\s*none/);
  });

  it("leaves the panel's last rows clear of the Copilot button, not of a map", () => {
    const open = rule(".lane-body.problems-open .problems-panel");
    const bottom = /padding-bottom:\s*([^;]+);/.exec(open)?.[1] ?? "";
    expect(bottom).not.toMatch(/108px/);
    // The button's 48px and its 18px lift.
    expect(bottom).toMatch(/48px \+ 18px/);
  });
});
