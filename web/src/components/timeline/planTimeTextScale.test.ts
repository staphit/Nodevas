import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the style sheets. A
// planned card's time (10:00 AM) sat at a fixed 9px while its row siblings
// (the Expected badge, the status label, the title) scaled with Settings ›
// Text size: at 150% the time was visibly tiny beside 12.75px–16.5px text.
function sheet(name: string): string {
  return readFileSync(resolve(process.cwd(), `src/styles/${name}`), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );
}

function fontSizes(css: string, selector: string): string[] {
  const sizes: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (!selectors.includes(selector)) continue;
    const size = /font-size:\s*([^;]+)/.exec(match[2]);
    if (size) sizes.push(size[1].trim());
  }
  return sizes;
}

const sheets = ["10f-canvas-overrides.css", "12-timeline.css"];

describe("planned card time follows the text size", () => {
  it("every .plan-time font size is scaled by --font-scale", () => {
    const sizes = sheets.flatMap((name) => fontSizes(sheet(name), ".plan-time"));
    expect(sizes.length).toBeGreaterThan(0);
    for (const size of sizes) expect(size).toMatch(/calc\([^]*var\(--font-scale/);
  });
});
