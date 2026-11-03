import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the style sheets. A
// button, field or select takes its font size from the user agent (13.33px),
// not from its parent, so any that no rule sizes ignored Settings › Text size:
// at 150% the command palette's result title stayed 13.33px under a 16.5px
// subtitle, and the drawer's "Add criterion" and the folded timeline's
// "Show timeline" stayed small.
function sheet(name: string): string {
  return readFileSync(resolve(process.cwd(), `src/styles/${name}`), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );
}

function rules(css: string, selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

const scaled = /font-size:\s*calc\([^;]*var\(--font-scale/;

describe("controls follow the text size", () => {
  it.each(["button", "input", "select", "textarea"])(
    "a bare %s is sized from the text-size setting",
    (selector) => {
      expect(rules(sheet("01-tokens.css"), selector)).toMatch(scaled);
    },
  );

  it("command palette results size their title, not leave it to the button", () => {
    const css = sheet("10a-canvas-tools.css");
    expect(rules(css, ".command-palette-results button")).toMatch(scaled);
    expect(rules(css, ".command-palette-results b")).toMatch(scaled);
  });

  it("the folded pane's Show button scales", () => {
    expect(rules(sheet("10f-canvas-overrides.css"), ".board-show-pane")).toMatch(scaled);
  });

  it("Add criterion is themed and scales", () => {
    const declarations = rules(sheet("28-roles.css"), ".acceptance-add button");
    expect(declarations).toMatch(scaled);
    expect(declarations).toMatch(/border:\s*1px solid var\(--border\)/);
    expect(declarations).toMatch(/color:\s*var\(--text\)/);
  });
});
