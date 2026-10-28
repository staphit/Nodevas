import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 10a-canvas-tools.css:
// a search hit whose snippet is one long line ends in an ellipsis inside the
// palette instead of widening its row and scrolling the list sideways.
const css = readFileSync(resolve(process.cwd(), "src/styles/10a-canvas-tools.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\>]/g, "\$&").replace(/\s+/g, "\s*");
  const match = new RegExp(`(^|\})\s*${escaped}\s*\{([^}]*)\}`, "m").exec(css);
  return match ? match[2] : "";
}

describe("command palette result rows", () => {
  it("let the text column shrink below its one-line width", () => {
    // A bare 1fr track is minmax(auto, 1fr): it never gets narrower than the
    // unwrapped snippet, so the ellipsis never applies.
    const columns = /grid-template-columns:\s*([^;]+);/.exec(rule(".command-palette-results button"));
    expect(columns?.[1]).toMatch(/^28px\s+minmax\(0,\s*1fr\)\s+auto$/);
  });
});
