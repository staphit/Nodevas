import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom paints nothing, so this reads the contract from the stylesheets. A
// vertical timeline's date label sticks to the side of the board, and the
// lanes scroll under it. Fading a future date with `opacity` faded its
// background too, so a lane's card showed through the label it passed under.
// A future date is dimmed in its text only; the label itself stays opaque.
const styles = ["10d-canvas-chrome.css", "10f-canvas-overrides.css", "12-timeline.css"].map(
  (name) => readFileSync(resolve(process.cwd(), "src/styles", name), "utf8"),
);

function rules(selector: string): string[] {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s+");
  const pattern = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "gm");
  return styles.flatMap((css) => Array.from(css.matchAll(pattern), (match) => match[2]));
}

describe("a vertical timeline's future date label", () => {
  const future = rules(".board-date-r.future");

  it("is still set apart from today and the past", () => {
    expect(future.join("\n")).toMatch(/font-style:\s*italic/);
    expect(future.join("\n")).toMatch(/color:/);
  });

  it("does not let the lanes passing under it show through", () => {
    for (const body of future) expect(body).not.toMatch(/(^|[\s;])opacity\s*:/);
    expect(rules(".board-date-r").join("\n")).toMatch(/background:\s*var\(--surface-2\)/);
  });
});
