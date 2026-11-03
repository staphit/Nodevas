import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// 1280x800 at 150% text with a long status badge: the bar squeezed the
// project menu (flex 0 1 auto, min-width 0) below its trigger's width, and the
// trigger spilled over Board (x 129–295 over 241–321), so a click on "Bo"
// opened the project menu. jsdom applies no CSS, so the contract is read from
// the stylesheet: the trigger is a flex item of the menu and shrinks with it,
// truncating the name, instead of overflowing it.
const css = readFileSync(resolve(process.cwd(), "src/styles/02-shell.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

/** Declarations of selector's rules outside any @media block. */
function topLevelRule(selector: string): string {
  let depth = 0;
  let mediaDepth = -1;
  let out = "";
  const re = /([^{}]*)\{|\}/g;
  let lastSelector = "";
  let bodyStart = 0;
  for (let match = re.exec(css); match; match = re.exec(css)) {
    if (match[0] === "}") {
      depth -= 1;
      if (depth === mediaDepth) mediaDepth = -1;
      else if (mediaDepth === -1 && depth === 0 && lastSelector.split(",").some((part) => part.trim() === selector))
        out += `${css.slice(bodyStart, match.index)};`;
      continue;
    }
    const head = match[1].trim();
    if (head.startsWith("@media")) mediaDepth = depth;
    depth += 1;
    lastSelector = head;
    bodyStart = re.lastIndex;
  }
  return out;
}

describe("project menu in a squeezed top bar", () => {
  it("lays its trigger out as a flex item, so the trigger shrinks with the menu", () => {
    expect(topLevelRule(".topbar-project")).toMatch(/(?:^|[;\s])display:\s*flex\b/);
  });

  it("lets the trigger and the name inside it shrink to the menu's width", () => {
    expect(topLevelRule(".topbar-project-trigger")).toMatch(/(?:^|[;\s])min-width:\s*0\b/);
    expect(topLevelRule(".topbar-project-name")).toMatch(/text-overflow:\s*ellipsis/);
  });
});
