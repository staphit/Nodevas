import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { COMPOSER_MAX_ROWS } from "./Composer";

// jsdom applies no CSS, so this reads the box's limit from 15-copilot.css.
const css = readFileSync(resolve(process.cwd(), "src/styles/15-copilot.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

/** Every declaration of property in a rule whose selector list holds selector. */
function declarations(selector: string, property: string): string[] {
  const found: string[] = [];
  const pattern = /([^{}]+)\{([^{}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (!selectors.includes(selector)) continue;
    for (const line of match[2].split(";")) {
      const [name, ...value] = line.split(":");
      if (name.trim() === property) found.push(value.join(":").trim());
    }
  }
  return found;
}

describe("the copilot text box", () => {
  it(`grows to ${COMPOSER_MAX_ROWS} lines at every font size, phones' 16px included`, () => {
    const [maxHeight] = declarations(".copilot-input", "max-height");
    const [lineHeight] = declarations(".copilot-input", "line-height");
    // In em the limit follows the box's own font size, which the phone rule
    // raises to 16px; a fixed 13px stopped it at about six and a half lines.
    // 18px is the padding (8px top and bottom) and the 1px border, both sides.
    expect(maxHeight).toBe(`calc(1em * ${lineHeight} * ${COMPOSER_MAX_ROWS} + 18px)`);
    expect(declarations(".copilot-input", "padding")).toEqual(["8px 10px"]);
    expect(declarations(".copilot-input", "border")[0]).toMatch(/^1px /);
  });
});
