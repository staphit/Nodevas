import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the composer's copilot clearance from
// 32-run-dialog.css. Only dialogs that can reach the bottom-right corner (the
// station's lists, the story room) give up room to the floating button; a
// composer in a centred modal (the start screen's describe box) keeps its
// full width.
const css = readFileSync(resolve(process.cwd(), "src/styles/32-run-dialog.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function clearanceSelectors(): string[] {
  const found: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    if (!/padding-right:\s*var\(--copilot-fab-clearance/.test(match[2])) continue;
    for (const selector of match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "))) {
      found.push(selector);
    }
  }
  return found;
}

describe("run dialog composer and the copilot button", () => {
  it("keeps Send and End clear of the button in the station and the story room", () => {
    const selectors = clearanceSelectors();
    expect(selectors.some((s) => s.includes(".station") && s.endsWith(".run-dlg-composer-actions"))).toBe(true);
    expect(selectors.some((s) => s.includes(".px-story-talk") && s.endsWith(".run-dlg-composer-actions"))).toBe(true);
  });

  it("pads no composer that is not in a place the button floats over", () => {
    const offenders = clearanceSelectors().filter(
      (s) => s.includes("run-dlg") && !(s.includes(".station") || s.includes(".px-story-talk")),
    );
    expect(offenders).toEqual([]);
  });
});
