import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// jsdom applies no CSS, so this reads the contract from 03-explorer.css: the
// explorer's 專案檔案 / 儲存 / 另存新檔 / 匯入 / 匯出 bar stays one row at text
// size 150% in a narrow sidebar. Labels never break between characters; when
// the row runs out of room the caption gives way first, then each label ends
// in an ellipsis.
const css = readFileSync(resolve(process.cwd(), "src/styles/03-explorer.css"), "utf8");

/** The declarations of the rule whose selector list is exactly `selector`. */
function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&").replace(/\s+/g, "\\s*");
  const match = new RegExp(`(^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(css);
  return match ? match[2] : "";
}

describe("project transfer bar layout", () => {
  it("keeps the row on one line", () => {
    expect(rule(".explorer-transfer-actions")).toMatch(/flex-wrap:\s*nowrap/);
    expect(rule(".explorer-transfer-actions")).toMatch(/min-width:\s*0/);
    expect(rule(".explorer-transfer-actions > div")).toMatch(/flex-wrap:\s*nowrap/);
    expect(rule(".explorer-transfer-actions > div")).toMatch(/min-width:\s*0/);
  });

  it("never breaks a label one character per line", () => {
    for (const selector of [
      ".explorer-transfer-actions > span",
      ".explorer-transfer-actions button",
      ".transfer-menu > summary",
    ]) {
      const body = rule(selector);
      expect(body, selector).toMatch(/white-space:\s*nowrap/);
      expect(body, selector).toMatch(/overflow:\s*hidden/);
      expect(body, selector).toMatch(/text-overflow:\s*ellipsis/);
      expect(body, selector).toMatch(/min-width:\s*0/);
    }
  });

  it("lets the caption give way before the buttons do", () => {
    const caption = /flex:\s*0\s+(\d+)/.exec(rule(".explorer-transfer-actions > span"));
    expect(caption).not.toBeNull();
    expect(Number(caption![1])).toBeGreaterThan(1);
    expect(rule(".transfer-menu")).toMatch(/min-width:\s*0/);
  });
});
