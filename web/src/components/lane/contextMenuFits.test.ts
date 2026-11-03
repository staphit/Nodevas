import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheet. The
// board's menus are placed on screen by their measured size, but one taller
// than the window has no placement that fits: it keeps its top edge and must
// scroll inside itself. The plan menu had no max-height and no overflow, so at
// 150% text its error ran off the bottom of an 800px window.
const css = readFileSync(resolve(process.cwd(), "src/styles/12-timeline.css"), "utf8");

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s+");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

describe("every board menu fits the window", () => {
  const menu = rule(".lane-context-menu");

  it("is never taller than the window less its margins", () => {
    expect(menu).toMatch(/max-height:\s*calc\(100vh - 16px\)/);
  });

  it("scrolls what does not fit", () => {
    expect(menu).toMatch(/overflow-y:\s*auto/);
  });
});
