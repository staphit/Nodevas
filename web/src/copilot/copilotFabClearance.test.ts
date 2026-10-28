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
    // The last row of an opened "More properties": its Delete node button
    // ends at the drawer's bottom right, under the button ("Delete [mic]e").
    ".app:has(> .copilot-fab) .drawer .meta-actions",
    ".app:has(> .copilot-fab) .lane-wrap.timeline.pane-collapsed > .board-title",
  ])("%s keeps clear of the button", (selector) => {
    expect(rules(selector)).toMatch(/padding-right:\s*var\(--copilot-fab-clearance\)/);
  });

  it("never moves the button left of where an open document's footer keeps clear", () => {
    const pattern = /([^{}]+)\{([^}]*)\}/g;
    const offenders: string[] = [];
    for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
      if (!/(^|[\s;])right:\s*calc\(/.test(match[2])) continue;
      for (const selector of match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "))) {
        if (!selector.endsWith("> .copilot-fab")) continue;
        if (!selector.includes(":not(:has(.drawer:not(.collapsed-rail)))")) offenders.push(selector);
      }
    }
    expect(offenders).toEqual([]);
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

  // The button floats over the stage's bottom-right corner, which is where
  // the graph's minimap sits while the timeline below it is folded, and where
  // the timeline's date ruler ends: it covered the minimap's lower edge and
  // the last dates' labels.
  it("lifts the graph's minimap clear of the button unless an open timeline sits below it", () => {
    const lifted = rules(
      ".app:has(> .copilot-fab) .lane-wrap.graph:not(:has(~ .lane-wrap.timeline:not(.pane-collapsed))) .graph-minimap",
    );
    const bottom = /bottom:\s*calc\((\d+)px\s*\+\s*(\d+)px\s*\+\s*(\d+)px\)/.exec(lifted);
    expect(bottom).not.toBeNull();
    // The button's height and its inset, with room between.
    const [, height, inset, air] = bottom!.map(Number);
    expect(height).toBe(48);
    expect(inset).toBe(18);
    expect(air).toBeGreaterThan(0);
  });

  it("gives the timeline room to scroll its last dates out from under the button", () => {
    expect(rules(".app:has(> .copilot-fab) .lane-wrap.timeline .board")).toMatch(
      /padding-bottom:\s*calc\(48px \+ 18px/,
    );
  });

  // The vertical timeline's date ruler is sticky at its right edge: rows
  // scroll, the ruler never does, so a button over it hides a date for good.
  // While such a timeline is open the button stands left of the ruler.
  // An open document covers the stage's right side, ruler and all, and its
  // footer keeps clear of the button where it normally stands: moved left for
  // a ruler the document hides, the button covered the footer's "S 立即儲存"
  // and the "類型、優先度、標籤… ›" summary.
  it("stands left of an open timeline's date ruler unless a document covers it", () => {
    const moved = rules(
      ".app:has(.lane-wrap.timeline:not(.pane-collapsed) .board-inner.timeline-vertical:not(.vdate-left)):not(:has(.drawer:not(.collapsed-rail))) > .copilot-fab",
    );
    expect(moved).toMatch(/right:\s*calc\(max\(18px, env\(safe-area-inset-right, 0px\)\)\s*\+\s*56px\s*\+/);
  });
});
