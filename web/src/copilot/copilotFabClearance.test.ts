import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 15-copilot.css. The
// voice copilot's button is fixed at the bottom right, which is exactly where
// the open document's footer sits (自動儲存 Ctrl/⌘+S, the save status, the
// 更多屬性 summary) and, on a narrow window, the collapsed timeline's
// "Show timeline" button. Each of those rows keeps clear of the button, and with a
// document open the button takes its round form, so what it needs is small.
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

describe("voice copilot button clearance", () => {
  it("says how much room the button takes while it is on screen", () => {
    expect(rules(".app:has(> .copilot-fab)")).toMatch(/--copilot-fab-clearance:/);
  });

  it.each([
    ".app:has(> .copilot-fab) .drawer .tab-footer",
    ".app:has(> .copilot-fab) .drawer .drawer-more-props > summary",
    ".app:has(> .copilot-fab) .lane-wrap.timeline.pane-collapsed > .board-title",
  ])("%s keeps clear of the button", (selector) => {
    expect(rules(selector)).toMatch(/padding-right:\s*var\(--copilot-fab-clearance\)/);
  });

  it("goes round while a document is open", () => {
    expect(rules(".app:has(.drawer:not(.collapsed-rail)) > .copilot-fab")).toMatch(/width:\s*48px/);
  });

  // On a phone the open document fills the screen and the keyboard lifts the
  // editor's last lines to where the button floats: it covered the text being
  // typed. While a document is being edited on a narrow screen the button
  // steps aside, and anywhere the editor leaves room below its last line.
  it("steps aside while a document is being edited on a phone", () => {
    const phone = /@media \(max-width:\s*(\d+)px\)\s*\{([^@]*)/g;
    const hidden: string[] = [];
    for (let match = phone.exec(css); match; match = phone.exec(css)) {
      if (Number(match[1]) < 500) continue;
      const block = /([^{}]*\.drawer[^{}]*cm-focused[^{}]*>\s*\.copilot-fab[^{}]*)\{([^}]*)\}/.exec(match[2]);
      if (block) hidden.push(block[2]);
    }
    expect(hidden.join(" ")).toMatch(/visibility:\s*hidden/);
    expect(hidden.join(" ")).toMatch(/pointer-events:\s*none/);
  });

  it("leaves room below the editor's last line for the button", () => {
    expect(rules(".app:has(> .copilot-fab) .drawer .cm-content")).toMatch(
      /padding-bottom:\s*calc\(48px/,
    );
  });
});
