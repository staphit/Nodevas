import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 03-explorer.css. A
// project row is [toggle][folder][name + 目前/資料夾 tag][count][+]. At text
// size 150% in the default 304px sidebar the tag and the count left the name
// a few letters ("gate…  Current 2000"). The tag is what yields: it wraps under
// the name when the two do not fit side by side, and only a name longer than
// the whole row is cut to an ellipsis.
const css = readFileSync(resolve(process.cwd(), "src/styles/03-explorer.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

/** Every declaration block whose selector list includes `selector`, joined. */
function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

describe("explorer project row", () => {
  it("wraps the tag under the name instead of squeezing the name", () => {
    expect(rules(".project-tree-open")).toMatch(/flex-wrap:\s*wrap/);
  });

  it("sizes the name by its text, so it is the tag that moves to the next line", () => {
    // `flex: 1` is a zero basis: the name never claims its width, nothing ever
    // wraps, and the name is what gets cut.
    const name = rules(".project-tree-name");
    expect(name).toMatch(/flex:\s*1 1 auto/);
    expect(name).toMatch(/max-width:\s*100%/);
  });
});
