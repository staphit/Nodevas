import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet.
// "More properties" opened takes up to 55% of the node panel, and the content
// view above it had no floor: at 1280x800 and 150% text its fixed rows took
// what was left and the editor came down to one line (21.8px). Opened, the
// properties keep to what the content view leaves them and scroll.
const css = readFileSync(resolve(process.cwd(), "src/styles/05-drawer.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

it("keeps the content view a floor while More properties is open", () => {
  const rule = /\.tab-body:has\(> \.drawer-more-props\[open\]\) > \.node-content-view \{([^}]*)\}/.exec(css);
  expect(rule).not.toBeNull();
  const floor = /min-height:\s*(\d+)%/.exec(rule?.[1] ?? "");
  expect(Number(floor?.[1])).toBeGreaterThanOrEqual(50);
});

it("leaves the properties a scroller, so they give way rather than the editor", () => {
  const rule = /\.drawer-more-props\[open\] \{([^}]*)\}/.exec(css);
  expect(rule?.[1]).toMatch(/flex:\s*0 1 auto/);
  expect(rule?.[1]).toMatch(/overflow-y:\s*auto/);
});
