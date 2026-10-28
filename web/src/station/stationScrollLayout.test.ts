import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 25-station.css.
// Station is meant to be one scroll container. It was three: the office's
// absolutely positioned art took .main (position: relative, and a scroller
// because of its overflow-x) as its containing block, so it hung out of
// .station's scroll box and made .main scroll too; and the side column was
// sized to 100vh, which ignores the top bar and the Station header above it,
// so it scrolled on its own and its last card, the agent chat's composer, sat
// below the bottom of the window.
const css = readFileSync(resolve(process.cwd(), "src/styles/25-station.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "")
  // Desktop only: the one-column layout under 900px resets all of this.
  .replace(/@media[^{]*\{(?:[^{}]*\{[^}]*\})*[^{}]*\}/g, "");

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

describe("Station scrolling", () => {
  it("keeps everything it draws inside its own scroll box", () => {
    expect(rules(".station")).toMatch(/position:\s*relative/);
  });

  it("sizes the side column to Station's own height, not the window's", () => {
    expect(rules(".station")).toMatch(/container-type:\s*size/);
    const side = rules(".station-side");
    expect(side).not.toMatch(/100vh/);
    expect(side).toMatch(/max-height:\s*100cqh/);
    // Level with the header, so a column as tall as Station fits from the top.
    expect(side).toMatch(/grid-row:\s*1\s*\/\s*span 3/);
  });

  it("lets the open agent chat give up height instead of the column scrolling", () => {
    expect(rules(".station-side > .station-network.is-open")).toMatch(/min-height:\s*0/);
  });
});
