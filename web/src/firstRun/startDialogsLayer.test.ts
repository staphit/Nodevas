import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so the stacking contract is read from the sheets.
function sheet(file: string): string {
  return readFileSync(resolve(process.cwd(), "src/styles", file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}

/** The z-index the last rule naming selector gives, as the cascade would. */
function zIndex(css: string, selector: string): number | undefined {
  let found: number | undefined;
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    if (!match[1].split(",").map((part) => part.trim()).includes(selector)) continue;
    for (const decl of match[2].split(";")) {
      const [name, value] = decl.split(":");
      if (name.trim() === "z-index") found = Number(value.trim());
    }
  }
  return found;
}

describe("dialogs opened over the start screen and the first-run questions", () => {
  // Add agent, set one up and pick a folder are portaled to body with
  // confirm-backdrop (80); the start screen's scrim is 90, so they opened
  // hidden behind it and could not be clicked.
  it("sit above the start screen's scrim and below the tour", () => {
    const scrim = zIndex(sheet("24-start-screen.css"), ".start-screen-backdrop");
    const dialogs = zIndex(sheet("24-start-screen.css"), ".confirm-backdrop.repo-add-backdrop");
    const tour = zIndex(sheet("13-tour.css"), ".tour-backdrop") ?? 130;
    expect(scrim).toBe(90);
    expect(dialogs).toBeGreaterThan(scrim ?? Infinity);
    expect(dialogs).toBeLessThan(tour);
  });
});
