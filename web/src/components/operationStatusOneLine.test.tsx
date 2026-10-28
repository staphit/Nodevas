import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { OperationStatus } from "./InteractionPrimitives";

// jsdom applies no CSS, so the contract is read from the stylesheets: the
// top bar's status badge is one line at every width (a long failure ends in an
// ellipsis, its whole text in the tooltip), not only on a phone.
const styles = (file: string) => readFileSync(resolve(process.cwd(), "src/styles", file), "utf8");

/** Declarations of selector's rules outside any @media block. */
function topLevelRule(css: string, selector: string): string {
  let depth = 0;
  let mediaDepth = -1;
  let out = "";
  const re = /([^{}]*)\{|\}/g;
  let match: RegExpExecArray | null;
  let lastSelector = "";
  let bodyStart = 0;
  while ((match = re.exec(css))) {
    if (match[0] === "}") {
      depth -= 1;
      if (depth === mediaDepth) mediaDepth = -1;
      else if (mediaDepth === -1 && depth === 0 && lastSelector === selector) out += css.slice(bodyStart, match.index);
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

describe("operation status badge", () => {
  const LONG =
    'load project "Disk project": graph.yaml: yaml: unmarshal errors: line 16: field start not found in type engine.Node';

  it("carries its whole message as a tooltip and keeps the text in its own box", () => {
    render(<OperationStatus status="error" message={LONG} />);
    const badge = screen.getByRole("alert");
    expect(badge.getAttribute("title")).toBe(LONG);
    expect(badge.querySelector(".operation-status-text")?.textContent).toBe(LONG);
  });

  it("stays one line in the top bar at desktop width", () => {
    const css = ["02-shell.css", "10a-canvas-tools.css"].map(styles).join("\n");
    const badge = topLevelRule(css, ".topbar .operation-status");
    expect(badge).toMatch(/min-width:\s*0/);
    const text = topLevelRule(css, ".operation-status-text");
    expect(text).toMatch(/white-space:\s*nowrap/);
    expect(text).toMatch(/overflow:\s*hidden/);
    expect(text).toMatch(/text-overflow:\s*ellipsis/);
  });
});
