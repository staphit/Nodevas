import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 02-shell.css. The top
// bar's 搜尋或指令 button and the plain buttons and selects of Settings ›
// General (切換為淺色, 重新播放導覽, 語系 …) carry no class of their own; with no
// rule for them outside the phone media query they rendered as grey user-agent
// boxes with an outset border on the dark theme, and their labels ignored the
// text-size setting.
const css = readFileSync(resolve(process.cwd(), "src/styles/02-shell.css"), "utf8");

/** The file with comments and every @media block taken out: what applies at desktop width. */
function topLevel(source: string): string {
  const text = source.replace(/\/\*[\s\S]*?\*\//g, "");
  let out = "";
  for (let index = 0; index < text.length; ) {
    if (text.startsWith("@media", index)) {
      let depth = 0;
      let cursor = text.indexOf("{", index);
      for (; cursor < text.length; cursor += 1) {
        if (text[cursor] === "{") depth += 1;
        else if (text[cursor] === "}" && --depth === 0) break;
      }
      index = cursor + 1;
      continue;
    }
    out += text[index];
    index += 1;
  }
  return out;
}

/** Every desktop declaration block whose selector list includes `selector`, joined. */
function desktopRules(selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  const source = topLevel(css);
  for (let match = pattern.exec(source); match; match = pattern.exec(source)) {
    const selectors = match[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

function expectThemedControl(selector: string) {
  const declarations = desktopRules(selector);
  expect(declarations, selector).toMatch(/border:\s*1px solid var\(--border\)/);
  expect(declarations, selector).toMatch(/background:\s*var\(--surface\)/);
  expect(declarations, selector).toMatch(/color:\s*var\(--text\)/);
  expect(declarations, selector).toMatch(/font-size:\s*calc\([^;]*var\(--font-scale/);
}

describe("controls without a class of their own", () => {
  it("the top bar's search-or-command button is themed at desktop width", () => {
    expectThemedControl(".topbar .command-trigger");
  });

  it("Settings rows' plain buttons and selects are themed and follow the text size", () => {
    expectThemedControl(".settings-row > button:not([class])");
    expectThemedControl(".settings-row > select");
  });
});
