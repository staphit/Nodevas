import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet itself:
// below 720px the board header's controls are one strip that scrolls sideways.
const css = readFileSync(
  resolve(process.cwd(), "src/styles/10f-canvas-overrides.css"),
  "utf8",
);

/** The body of the first block at or after `from` whose prelude matches. */
function block(prelude: RegExp, source: string, from = 0): string {
  prelude.lastIndex = from;
  const match = prelude.exec(source);
  if (!match) return "";
  let depth = 0;
  const start = source.indexOf("{", match.index);
  for (let at = start; at < source.length; at += 1) {
    if (source[at] === "{") depth += 1;
    if (source[at] === "}") depth -= 1;
    if (depth === 0) return source.slice(start + 1, at);
  }
  return "";
}

describe("board header strip on a phone", () => {
  const phone = block(/@media \(max-width: 720px\) \{/g, css);
  const strip = block(/(^|\n)\s*\.board-title-controls \{/g, phone);

  it("scrolls sideways", () => {
    expect(strip).toMatch(/overflow-x:\s*auto/);
  });

  // The pane chevron's 44px tap overlay sticks out above and below a strip
  // only ~24px tall. With overflow-x alone the other axis computes to auto as
  // well, so a collapsed timeline's header grew its own tiny vertical
  // scrollbar, whose arrows then took width from the pane name.
  it("never scrolls up and down", () => {
    expect(strip).toMatch(/overflow-y:\s*(hidden|clip)/);
  });
});
