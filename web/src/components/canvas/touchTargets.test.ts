import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 14-touch.css itself:
// the buttons that open and close a whole panel are a finger's 44px wherever
// a finger is the pointer or the screen is a phone's.
const css = readFileSync(resolve(process.cwd(), "src/styles/14-touch.css"), "utf8");

/** The body of the first block whose prelude matches, braces balanced. */
function block(prelude: RegExp): string {
  const match = prelude.exec(css);
  if (!match) return "";
  let depth = 0;
  const start = css.indexOf("{", match.index);
  for (let at = start; at < css.length; at += 1) {
    if (css[at] === "{") depth += 1;
    if (css[at] === "}") depth -= 1;
    if (depth === 0) return css.slice(start + 1, at);
  }
  return "";
}

describe("touch targets", () => {
  it("squares the panel toggles up to 44px on a coarse pointer and on a phone", () => {
    const body = block(/@media \(pointer: coarse\), \(max-width: 720px\)/);
    for (const selector of [".explorer-collapse", ".board-explorer-toggle", ".problems-close"]) {
      expect(body).toContain(selector);
    }
    expect(body).toMatch(/min-width:\s*44px/);
    expect(body).toMatch(/min-height:\s*44px/);
  });

  it("sizes the board's » with the board's tool buttons", () => {
    const body = block(/@media \(pointer: coarse\) \{/);
    expect(body).toMatch(/\.board-tool-button,\s*\.board-explorer-toggle,/);
  });
});
