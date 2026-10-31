import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet.
// The board gains `.panning` when a drag-pan starts and loses it when it ends.
// `.board.panning *` made each toggle restyle every element on the board —
// 5600 of them on a 2000-card board at 50%, a 25–40ms frame at each end of
// every pan. The cursor is inherited, so the board sets it once and only the
// cards, which set their own, are told to take it.
const css = readFileSync(resolve(process.cwd(), "src/styles/12-timeline.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

describe("panning cursor", () => {
  it("does not restyle every element on the board when a pan starts or ends", () => {
    expect(css).not.toMatch(/\.panning\s+\*/);
  });

  it("still shows the grabbing cursor over the board and its cards", () => {
    const rules = [...css.matchAll(/([^{}]*\.board\.panning[^{}]*)\{([^}]*)\}/g)];
    const selectors = rules.flatMap((rule) => rule[1].split(",").map((s) => s.trim()));
    expect(selectors).toContain(".board.panning");
    expect(selectors).toContain(".board.panning .col-card");
    for (const rule of rules) expect(rule[2]).toMatch(/cursor:\s*grabbing/);
  });
});
