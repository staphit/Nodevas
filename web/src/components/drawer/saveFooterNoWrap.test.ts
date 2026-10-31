import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 09-editor-preview.css.
// Offline, the save row holds the red badge (無法連線到伺服器), "Retrying
// automatically", a Retry now button and the keyboard hint. The hint would not
// wrap, so in a 580px panel at 150% text the flex row squeezed everything else
// to its min-content: the badge stood eight lines tall, one character a line,
// the button 36x104px, and the row grew to ~180px. The badge, the retry note
// and the button keep to one line each; the row wraps, and the hint is what
// gives way, wrapping onto its own line.
const css = readFileSync(resolve(process.cwd(), "src/styles/09-editor-preview.css"), "utf8").replace(
  /\/\*[\s\S]*?\*\//g,
  "",
);

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`(?:^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`).exec(css);
  expect(match, `${selector} rule`).not.toBeNull();
  return match?.[1] ?? "";
}

it("lets the save row wrap instead of crushing its items", () => {
  expect(rule(".tab-footer")).toMatch(/flex-wrap:\s*wrap/);
});

it("keeps the save badge on one line, never shrunk below its text", () => {
  const badge = rule(".tab-footer .operation-status");
  expect(badge).toMatch(/flex:\s*0 0 auto/);
  // A very long reason ends in an ellipsis inside the panel; the tooltip has it.
  expect(badge).toMatch(/max-width:\s*100%/);
  const text = rule(".tab-footer .operation-status-text");
  expect(text).toMatch(/white-space:\s*nowrap/);
  expect(text).toMatch(/text-overflow:\s*ellipsis/);
  expect(text).toMatch(/overflow:\s*hidden/);
});

it("keeps the retry note and the Retry now button on one line", () => {
  const retry = rule(".tab-footer .tab-footer-retry");
  expect(retry).toMatch(/white-space:\s*nowrap/);
  expect(retry).toMatch(/flex:\s*0 0 auto/);
  const button = rule(".tab-footer button");
  expect(button).toMatch(/white-space:\s*nowrap/);
  expect(button).toMatch(/flex:\s*0 0 auto/);
});

it("lets the keyboard hint be the part that wraps", () => {
  const hint = rule(".tab-footer .tab-footer-hint");
  expect(hint).not.toMatch(/white-space:\s*nowrap/);
  expect(hint).toMatch(/flex-wrap:\s*wrap/);
  expect(hint).toMatch(/min-width:\s*0/);
});
