import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// At 500px wide and 150% text, in English, the timeline header's strip was
// 1.45px too narrow once the pane name reached its floor (3.6em = 64.8px), and
// a whole scrollbar was drawn for it. The ⋯ menu, always last, gives up a few
// pixels of its width before the strip scrolls. jsdom applies no CSS, so the
// contract is read from the stylesheet.
const css = readFileSync(resolve(process.cwd(), "src/styles/10f-canvas-overrides.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

/** Every declaration in the phone media blocks for exactly `selector`. */
function phoneRules(selector: string): string {
  const out: string[] = [];
  const media = /@media \(max-width: 720px\) \{/g;
  for (let match = media.exec(css); match; match = media.exec(css)) {
    let depth = 0;
    const start = css.indexOf("{", match.index);
    let end = start;
    for (let at = start; at < css.length; at += 1) {
      if (css[at] === "{") depth += 1;
      if (css[at] === "}") depth -= 1;
      if (depth === 0) {
        end = at;
        break;
      }
    }
    const body = css.slice(start + 1, end);
    const rule = /([^{}]+)\{([^}]*)\}/g;
    for (let r = rule.exec(body); r; r = rule.exec(body)) {
      if (r[1].split(",").some((part) => part.trim() === selector)) out.push(r[2]);
    }
  }
  return out.join(";");
}

const px = (rules: string, property: string) => {
  const match = new RegExp(`(?:^|[;\\s])${property}:\\s*([\\d.]+)px`).exec(rules);
  return match ? Number(match[1]) : NaN;
};

describe("board header strip slack", () => {
  const menu = phoneRules(".board-title-controls > .board-overflow:last-child");
  const trigger = phoneRules(".board-title-controls > .board-overflow:last-child .board-overflow-trigger");

  it("lets the last ⋯ menu shrink a little before the strip scrolls", () => {
    expect(menu).toMatch(/flex-shrink:\s*1/);
    // Still a fingertip-sized target: no narrower than the ? toggle (28px).
    expect(px(menu, "min-width")).toBeGreaterThanOrEqual(28);
    expect(px(menu, "min-width")).toBeLessThanOrEqual(36 - 2);
  });

  it("has the menu's button follow its narrower box", () => {
    expect(trigger).toMatch(/min-width:\s*0/);
    expect(trigger).toMatch(/width:\s*100%/);
  });
});
