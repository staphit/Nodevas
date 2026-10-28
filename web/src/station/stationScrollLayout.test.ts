import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 25-station.css.
// Station is meant to be one scroll container. It was three: the office's
// absolutely positioned art took .main (position: relative, and a scroller
// because of its overflow-x) as its containing block, so it hung out of
// .station's scroll box and made .main scroll too; and the side column was
// sized to 100vh, which ignores the top bar and the Station header above it,
// so it scrolled on its own and its last card, the agent chat's composer, sat
// below the bottom of the window.
const css = readFileSync(resolve(process.cwd(), "src/styles/25-station.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "")
  // Desktop only: the one-column layout under 900px resets all of this.
  .replace(/@media[^{]*\{(?:[^{}]*\{[^}]*\})*[^{}]*\}/g, "");

function rules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(css); match; match = pattern.exec(css)) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

describe("Station scrolling", () => {
  it("keeps everything it draws inside its own scroll box", () => {
    expect(rules(".station")).toMatch(/position:\s*relative/);
  });

  it("sizes the side column to Station's own height, not the window's", () => {
    expect(rules(".station")).toMatch(/container-type:\s*size/);
    const side = rules(".station-side");
    expect(side).not.toMatch(/100vh/);
    expect(side).toMatch(/max-height:\s*100cqh/);
    // Level with the header, so a column as tall as Station fits from the top.
    expect(side).toMatch(/grid-row:\s*1\s*\/\s*span 3/);
  });

  it("lets the open agent chat fill the column's height", () => {
    expect(rules(".station-side:has(> .station-network.is-open)")).toMatch(
      /height:\s*100cqh/,
    );
    expect(rules(".station-side > .station-network.is-open")).toMatch(/flex:\s*1 0 auto/);
  });
});

// At 1280x800 the docked agent chat was squeezed into slivers: every section
// shrank together, so the how-to-connect intro got 117px of its 246px, the
// message list 37px of its 49px, each behind its own scrollbar, and the
// header's "Agent chat" wrapped while "0 online" ellipsised to "0.".
describe("Station's docked agent chat", () => {
  it("keeps its sections at their own height and lets the column scroll instead", () => {
    const panel = rules(".station-side > .station-network.is-open > .agent-panel.is-docked");
    expect(panel).toMatch(/flex:\s*1 0 auto/);
    expect(rules(".agent-panel.is-docked > *")).toMatch(/flex-shrink:\s*0/);
  });

  it("shows the whole connect intro, not a 200px window onto it", () => {
    expect(rules(".agent-panel.is-docked > .agent-roster-section.is-intro")).toMatch(
      /max-height:\s*none/,
    );
  });

  it("gives the message list room for its empty state and a few messages", () => {
    const list = rules(".agent-panel.is-docked > .agent-messages-list");
    const basis = list.match(/flex:\s*1 0 (\d+)px/);
    expect(basis).not.toBeNull();
    expect(Number(basis?.[1])).toBeGreaterThanOrEqual(120);
  });

  it("wraps the header's controls rather than the title or the online count", () => {
    expect(rules(".agent-panel.is-docked .copilot-header-row")).toMatch(/flex-wrap:\s*wrap/);
    expect(rules(".agent-panel.is-docked .copilot-heading")).toMatch(/white-space:\s*nowrap/);
    expect(rules(".agent-panel.is-docked .copilot-model")).toMatch(/flex:\s*1 0 auto/);
    // The card's own toggle row too: "Agent chat" stayed one word per line.
    expect(rules(".station-network-toggle")).toMatch(/flex-wrap:\s*wrap/);
    expect(rules(".station-network-name")).toMatch(/white-space:\s*nowrap/);
  });
});

// Under 1181px the voice copilot's button stays in the bottom right corner,
// which at the end of Station's scroll is the agent chat's Send button (at
// 500px, 150% text, elementFromPoint on Send returned the button). Station's
// end keeps the button's height clear, so the last row scrolls up past it.
describe("Station's end and the voice copilot button", () => {
  const raw = readFileSync(resolve(process.cwd(), "src/styles/25-station.css"), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );

  it("pads its bottom by the button's height wherever the button sits over it", () => {
    const media = raw.match(/@media \(max-width: 1180px\)\s*\{((?:[^{}]*\{[^}]*\})*)[^{}]*\}/);
    expect(media).not.toBeNull();
    const rule = media?.[1].match(/\.app\.mode-station:has\(> \.copilot-fab\) \.station\s*\{([^}]*)\}/);
    expect(rule?.[1]).toMatch(/padding-bottom:\s*calc\([^;]*48px[^;]*\)/);
  });
});
