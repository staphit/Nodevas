import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet.
// Beside an open node drawer at 150% text the horizontal timeline's corner is
// 98px wide, and its one-line label read "Nodes / da…" with nothing saying
// the rest. The corner is 48px tall, room for two lines: the label wraps
// there, and only a third line would be cut.
const css = readFileSync(resolve(process.cwd(), "src/styles/12-timeline.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

it("lets the corner label wrap to a second line instead of cutting it", () => {
  const rule = /\.horizontal-corner-label \{([^}]*)\}/.exec(css)?.[1] ?? "";
  expect(rule).not.toMatch(/white-space:\s*nowrap/);
  expect(rule).toMatch(/-webkit-line-clamp:\s*2/);
  expect(rule).toMatch(/overflow-wrap:\s*anywhere/);
});
