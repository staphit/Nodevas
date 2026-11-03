import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(process.cwd(), "src/styles/15-copilot.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

describe("15-copilot.css ask card overflow and wrap layout", () => {
  it("allows permission head to wrap chips and badges so title is not squeezed", () => {
    const head = rules(".copilot-permission-head");
    expect(head).toMatch(/flex-wrap:\s*wrap/);
  });

  it("applies word-break and overflow-wrap to summary", () => {
    const summary = rules(".copilot-permission-summary");
    expect(summary).toMatch(/word-break:\s*(break-word|break-all)/);
    expect(summary).toMatch(/overflow-wrap:\s*(break-word|anywhere)/);
  });

  it("applies word-break and overflow-wrap to chips", () => {
    const chip = rules(".copilot-permission .agent-node-chip") || rules(".agent-node-chip");
    expect(chip).toMatch(/word-break:\s*(break-word|break-all)/);
    expect(chip).toMatch(/overflow-wrap:\s*(break-word|anywhere)/);
  });
});
