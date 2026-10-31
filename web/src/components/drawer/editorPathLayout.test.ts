import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 08-card-shapes.css.
// The open document's path ("project / nodes/x.md") sits on one row beside
// the page menu. In a phone-width drawer a long project name wrapped onto
// several lines and pushed the toolbar down; it now keeps to one line and
// ends in an ellipsis, and the file name keeps its own share of the row.
const css = readFileSync(resolve(process.cwd(), "src/styles/08-card-shapes.css"), "utf8").replace(
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

describe("document path breadcrumb", () => {
  it("keeps a long project name to one line with an ellipsis", () => {
    const project = rules(".editor-path-project");
    expect(project).toMatch(/white-space:\s*nowrap/);
    expect(project).toMatch(/overflow:\s*hidden/);
    expect(project).toMatch(/text-overflow:\s*ellipsis/);
    expect(project).toMatch(/min-width:\s*0/);
  });

  it("lets the file name shrink too, and never the separator", () => {
    expect(rules(".editor-path-file")).toMatch(/min-width:\s*0/);
    expect(rules(".editor-path-sep")).toMatch(/flex-shrink:\s*0/);
  });
});
