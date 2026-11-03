import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheets.
// Every button shrinks while pressed. Done with `transform`, the press rule
// (button:active, specificity 0,1,1) replaces whatever transform a button is
// placed by: the runs view's "Jump to latest" sits centred by
// translateX(-50%), so on mousedown it moved right by half its width and a
// click on its left half landed on the log instead. The press is the `scale`
// property, which composes with a placement transform instead of replacing it.
const read = (file: string) =>
  readFileSync(resolve(process.cwd(), "src/styles", file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** Every declaration body whose selector list includes exactly `selector`. */
function bodiesOf(css: string, selector: string): string[] {
  const bodies: string[] = [];
  const pattern = /([^{}]+)\{([^{}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) bodies.push(match[2]);
  }
  return bodies;
}

const tokens = read("01-tokens.css");

describe("a pressed button", () => {
  it("shrinks by the scale property, never by replacing its transform", () => {
    const bodies = bodiesOf(tokens, "button:active");
    expect(bodies.length).toBeGreaterThan(0);
    for (const body of bodies) expect(body).not.toMatch(/(^|[;\s])transform\s*:/);
    expect(bodies.join("\n")).toMatch(/(^|[;\s])scale\s*:\s*0\.97/);
  });

  it("animates the scale it presses with", () => {
    const button = bodiesOf(tokens, "button").join("\n");
    expect(button).toMatch(/transition:[^;]*\bscale\s+120ms/);
  });

  it("does not shrink when motion is reduced", () => {
    const reduced = read("09-editor-preview.css");
    const block = /@media \(prefers-reduced-motion: reduce\)\s*\{([\s\S]*)$/.exec(reduced)?.[1] ?? "";
    expect(bodiesOf(block, "button:active").join("\n")).toMatch(/(^|[;\s])scale\s*:\s*none/);
  });

  it("keeps Jump to latest centred under the pointer while pressed", () => {
    const runs = read("18-runs.css");
    expect(bodiesOf(runs, ".run-jump").join("\n")).toMatch(/translateX\(-50%\)/);
    // No press rule anywhere hands the jump button another transform.
    for (const file of ["01-tokens.css", "18-runs.css"]) {
      for (const selector of ["button:active", ".run-jump:active"]) {
        for (const body of bodiesOf(read(file), selector)) {
          expect(body).not.toMatch(/(^|[;\s])transform\s*:/);
        }
      }
    }
  });
});
