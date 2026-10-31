import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 03-explorer.css: the
// explorer's tool rows (新增子專案 / 排序 / 匯入 Markdown …, and the
// 專案檔案 / 儲存 / 另存新檔 / 匯入 / 匯出 bar) keep every label whole at text
// size 90–150% in a sidebar 220–480px wide. A row that runs out of room wraps
// onto another line; a label is never cut to an ellipsis (匯入 and 匯出 both
// read "匯…" that way) and never shrinks below its own text.
const css = readFileSync(resolve(process.cwd(), "src/styles/03-explorer.css"), "utf8");

/** The declarations of the rule whose selector list is exactly `selector`. */
function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s*");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

/** Every declaration block in the file that styles `selector`, alone or in a list. */
function rulesMentioning(selector: string): string[] {
  const blocks: string[] = [];
  const source = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(source); match; match = pattern.exec(source)) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

/** A label that can be cut: an ellipsis, hidden overflow, or a shrink below its text. */
function expectNeverClipped(selector: string) {
  const blocks = rulesMentioning(selector);
  expect(blocks.length, selector).toBeGreaterThan(0);
  for (const body of blocks) {
    expect(body, selector).not.toMatch(/text-overflow:\s*ellipsis/);
    expect(body, selector).not.toMatch(/overflow:\s*hidden/);
    expect(body, selector).not.toMatch(/min-width:\s*0/);
  }
}

describe("project transfer bar layout", () => {
  it("wraps onto a second row instead of squeezing the labels", () => {
    expect(rule(".explorer-transfer-actions")).toMatch(/flex-wrap:\s*wrap/);
    expect(rule(".explorer-transfer-actions > div")).toMatch(/flex-wrap:\s*wrap/);
  });

  it("never cuts a label short", () => {
    for (const selector of [
      ".explorer-transfer-actions > span",
      ".explorer-transfer-actions button",
      ".transfer-menu",
      ".transfer-menu > summary",
    ]) {
      expectNeverClipped(selector);
    }
  });

  it("keeps each control at least as wide as its label", () => {
    // flex: none — a control on a crowded row moves to the next one whole.
    expect(rule(".explorer-transfer-actions button")).toMatch(/flex:\s*none/);
    expect(rule(".transfer-menu")).toMatch(/flex:\s*none/);
    expect(rule(".transfer-menu > summary")).toMatch(/white-space:\s*nowrap/);
    expect(rule(".explorer-transfer-actions button")).toMatch(/white-space:\s*nowrap/);
  });
});

describe("explorer tools layout", () => {
  it("flows the tools into as many rows as the width needs", () => {
    // A fixed three-column grid gave 新增子專案 a third of 220px, which at
    // 150% is narrower than the label.
    const body = rule(".explorer-create-actions");
    expect(body).toMatch(/display:\s*flex/);
    expect(body).toMatch(/flex-wrap:\s*wrap/);
    expect(body).not.toMatch(/grid-template-columns/);
    expect(rule(".explorer-create-actions > *")).toMatch(/flex:\s*1\s+1\s+auto/);
  });

  it("never cuts a tool's label short", () => {
    expectNeverClipped(".explorer-create-actions button");
    // A label longer than the whole row wraps inside its button rather than
    // running out of it.
    expect(rule(".explorer-create-actions button")).toMatch(/white-space:\s*normal/);
  });
});

describe("project transfer notice", () => {
  it("shows the whole reason, wrapped, rather than an ellipsis", () => {
    // An export that fails says why ("Document export failed: graph.yaml:
    // line 2 has an error. Fix the file, then reload."); cut to one line in a
    // 234px sidebar the reason was lost, with no tooltip to get it back.
    expectNeverClipped(".project-transfer-notice");
    const body = rule(".project-transfer-notice");
    expect(body).not.toMatch(/white-space:\s*nowrap/);
    expect(body).toMatch(/overflow-wrap:\s*anywhere/);
  });
});
