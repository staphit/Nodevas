import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the ask card's contract from the
// stylesheets.
function sheet(file: string): string {
  return readFileSync(resolve(process.cwd(), "src/styles", file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}

/** The value the last rule naming selector gives property, as the cascade would. */
function last(css: string, selector: string, property: string): string | undefined {
  let found: string | undefined;
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    if (!match[1].split(",").map((part) => part.trim()).includes(selector)) continue;
    for (const decl of match[2].split(";")) {
      const [name, ...value] = decl.split(":");
      if (name.trim() === property) found = value.join(":").trim();
    }
  }
  return found;
}

describe("ask card title", () => {
  // Next to the agent, tool, run, node and countdown chips, a title on a
  // zero flex basis got 10px of 309 and read "r…": the head's wrap never
  // wrapped, since a zero-basis item always fits.
  it("keeps a real width and wraps the chips instead of squeezing the title", () => {
    const css = sheet("15-copilot.css");
    const flex = last(css, ".copilot-permission-title", "flex");
    expect(flex).toBeDefined();
    expect(flex).not.toMatch(/^1$|\s0(px|%)?$|^1 1 0/);
    expect(last(css, ".copilot-permission-title", "min-width")).toMatch(/ch|em/);
  });
});

describe("Agent chat's asks section", () => {
  // Six asks at 1280x800 made it 1143px tall in an 800px drawer that does not
  // scroll: the lower cards and the composer were out of reach.
  it("is capped in height and scrolls its own cards", () => {
    const css = sheet("16-agents.css");
    expect(last(css, ".agent-asks-section", "max-height")).toMatch(/vh|cqh|px/);
    expect(last(css, ".agent-asks-section", "overflow-y")).toBe("auto");
    expect(last(css, ".agent-asks-section", "flex-shrink")).toBe("0");
  });
});
