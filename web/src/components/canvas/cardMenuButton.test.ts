import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from the stylesheets.
// Every button shrinks while pressed through `button:active { transform }`
// (01-tokens.css, specificity 0,1,1). A card's ⋯ button placed by its own
// `transform` (0,1,0) loses that transform on mousedown, jumps out from under
// the pointer, and the click lands on the board instead of opening the menu.
const board = readFileSync(resolve(process.cwd(), "src/styles/04-board.css"), "utf8");

/** Every top-level declaration body whose selector list is exactly `selector`. */
function bodiesOf(css: string, selector: string): string[] {
  const bodies: string[] = [];
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const prelude = new RegExp(`^${escaped}\\s*\\{([^}]*)\\}`, "gm");
  for (const match of css.matchAll(prelude)) bodies.push(match[1]);
  return bodies;
}

describe("card menu button", () => {
  it("is placed by the translate property, which a pressed button's scale cannot cancel", () => {
    const bodies = bodiesOf(board, ".col-card-more");
    expect(bodies.length).toBeGreaterThan(0);
    for (const body of bodies) {
      expect(body).not.toMatch(/(^|[;\s])transform\s*:/);
    }
    expect(bodies.join("\n")).toMatch(/(^|[;\s])translate\s*:\s*calc\(-100% - 6px\)\s+6px/);
  });
});
