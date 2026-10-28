import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom paints nothing, so this reads the contract from the stylesheet. The
// graph's alignment grid was two repeating gradients, each one image the size
// of the whole board. Chrome rasterises a background image no larger than its
// texture limit, so on the 2000-node project's 85,280px-wide board the lines
// were gone and a few pale vertical bands were left where its tiles met. Each
// line is now drawn once in a tile one cell in size, repeated across the board.
const css = readFileSync(resolve(process.cwd(), "src/styles/12-timeline.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s+");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

describe("the graph's alignment grid", () => {
  const grid = rule(".board-inner.graph-bg");

  it("is painted from cell-sized tiles, not board-sized images", () => {
    expect(grid).not.toMatch(/repeating-linear-gradient/);
    expect(grid).toMatch(/background-size:\s*var\(--col-w\)\s+var\(--step\)/);
    expect(grid).toMatch(/background-repeat:\s*repeat\b/);
  });

  it("still starts its cells at the graph's origin", () => {
    expect(grid).toMatch(
      /background-position:\s*var\(--grid-origin-x,\s*0\)\s+var\(--grid-origin-y,\s*0\)/,
    );
  });
});
