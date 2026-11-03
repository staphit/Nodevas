import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 10d-canvas-chrome.css.
// The subpage strip's rename field and its ← / → buttons carry no class of
// their own and the strip's rules set only their sizes, so they rendered as
// user-agent boxes (2px inset / outset borders, square corners, a grey fill)
// beside the themed format select and delete button.
const css = readFileSync(resolve(process.cwd(), "src/styles/10d-canvas-chrome.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

describe("subpage strip controls", () => {
  it.each([".content-page-manage input", ".content-page-manage button:not(.danger)"])(
    "%s is themed like the format select beside it",
    (selector) => {
      const declarations = rules(selector);
      expect(declarations).toMatch(/border:\s*1px solid var\(--border\)/);
      expect(declarations).toMatch(/border-radius:\s*4px/);
      expect(declarations).toMatch(/background:\s*var\(--surface\)/);
      expect(declarations).toMatch(/color:\s*var\(--text\)/);
      expect(declarations).toMatch(/font-size:\s*calc\([^;]*var\(--font-scale/);
    },
  );
});
