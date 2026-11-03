import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { translate } from "../../i18n";

const source = (path: string) => readFileSync(resolve(process.cwd(), "src", path), "utf8");

// A node row's tooltip is its title and how to select it. The parentheses
// were written around the translated hint in WorkspaceTree.tsx, so zh-TW read
// "Card 1 foxtrot (Ctrl／⌘ + 點擊多選，…)" with half-width ( ) around
// Chinese. The whole line is the catalog's, parentheses included.
describe("node row tooltip", () => {
  it("is one catalog line in each language", () => {
    expect(translate("explorer.nodeSelectionTitle", "en", { title: "Team sync" })).toBe(
      "Team sync (Ctrl/⌘ + click to multi-select, Shift + click to select a range, right-click for the file menu)",
    );
    expect(translate("explorer.nodeSelectionTitle", "zh-TW", { title: "Team sync" })).toBe(
      "Team sync（Ctrl／⌘ + 點擊多選，Shift + 點擊選取範圍，右鍵開啟檔案選單）",
    );
  });

  it("is not wrapped in parentheses outside the catalog", () => {
    const tree = source("components/explorer/WorkspaceTree.tsx");
    expect(tree).not.toMatch(/\(\$\{t\("explorer\.nodeSelectionTitle"\)\}\)/);
    expect(tree).toMatch(/t\("explorer\.nodeSelectionTitle", \{ title: node\.title \|\| node\.id \}\)/);
  });
});

// Every other notice that names a thing quotes it (已建立節點「…」。); the
// new-project notice read 「已建立並開啟 掃描四象限」 with the name bare.
describe("new project notice", () => {
  it("quotes the project's name", () => {
    expect(translate("explorer.createdProject", "zh-TW", { name: "掃描四象限" })).toBe("已建立並開啟「掃描四象限」。");
    expect(translate("explorer.createdProject", "en", { name: "Sweep" })).toBe("Created and opened “Sweep”.");
  });
});
