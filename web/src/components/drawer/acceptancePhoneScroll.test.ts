import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheets.
// On a phone the node's content view is the scroller (05-drawer.css), and the
// acceptance criteria kept their desktop cap of 40% with a scrollbar of their
// own: at 500x800 and 150% text they scrolled 168/151 inside a content view
// scrolling 470/381, and the new-criterion field and its button sat at the
// bottom of the inner box. On a phone they take their own height and scroll
// with the rest.
const read = (name: string) =>
  readFileSync(resolve(process.cwd(), "src/styles", name), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

function phoneBlock(css: string): string {
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

it("keeps the desktop cap, where the editor below needs the room", () => {
  expect(read("28-roles.css")).toMatch(/\.acceptance \{[^}]*max-height:\s*40%/);
});

it("lets the criteria scroll with the content view on a phone", () => {
  const block = phoneBlock(read("05-drawer.css"));
  const rule = /\.drawer \.acceptance \{([^}]*)\}/.exec(block);
  expect(rule).not.toBeNull();
  expect(rule?.[1]).toMatch(/max-height:\s*none/);
  expect(rule?.[1]).toMatch(/overflow:\s*visible/);
});
