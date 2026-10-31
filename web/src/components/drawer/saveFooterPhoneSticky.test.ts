import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 05-drawer.css.
// On a phone the node's content view is the scroller, and the document keeps
// a floor of 55% of it. The save row ("Auto-save Ctrl/⌘+S Save now") was the
// content view's last child: at 500x800 and 150% text the rows above and that
// floor came to 411px in a 381px view, so the row sat below the view's edge,
// clipped where the "More properties" bar begins. The row is the only place
// a save can be seen to have happened, so it stays on screen at the foot of
// the view while the rest scrolls.
const css = readFileSync(resolve(process.cwd(), "src/styles/05-drawer.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function phoneBlock(): string {
  const start = css.indexOf("@media (max-width: 720px) {");
  expect(start).toBeGreaterThanOrEqual(0);
  let depth = 0;
  for (let i = css.indexOf("{", start); i < css.length; i += 1) {
    if (css[i] === "{") depth += 1;
    if (css[i] === "}") depth -= 1;
    if (depth === 0) return css.slice(start, i + 1);
  }
  return "";
}

it("keeps the save row at the foot of the scrolling content view on a phone", () => {
  const block = phoneBlock();
  expect(block).toMatch(/\.drawer \.node-content-view \{[^}]*overflow-y:\s*auto/);
  const rule = /\.drawer \.node-content-view > \.tab-footer \{([^}]*)\}/.exec(block);
  expect(rule).not.toBeNull();
  expect(rule?.[1]).toMatch(/position:\s*sticky/);
  expect(rule?.[1]).toMatch(/bottom:\s*0/);
});
