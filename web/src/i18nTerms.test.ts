import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { translate } from "./i18n";

// Words a Traditional Chinese reader should not meet in English. Product
// and protocol names (Runner, Token, Git, Copilot, PDF…) stay as they are.
const KEYS = [
  "settingsPage.page.agents",
  "station.roster.title",
  "editor.markdown.task",
  "node.writeAccess.worker",
  "node.writeAccess.orchestrator",
  "auth.email",
  "merge.pullRequest",
  "merge.head",
  "routing.fallbackN",
  "autopilot.stage.commit",
  "autopilot.stageShort.commit",
  "autopilot.stage.push",
  "autopilot.stageShort.push",
  "autopilot.stage.pr",
  "evidence.head",
  "runners.link.accessClientSecret",
  "usage.tab.sessions",
  "usage.col.session",
  "usage.source.session",
  "usage.source.run",
  "explorer.frontMatter.assignee",
  "explorer.frontMatter.priority",
  "explorer.frontMatter.requires",
  "explorer.frontMatter.tags",
  "explorer.frontMatter.title",
  "explorer.frontMatter.kind",
];

describe("Traditional Chinese terms", () => {
  it.each(KEYS)("%s reads in Chinese", (key) => {
    expect(translate(key, "zh-TW")).toMatch(/\p{Script=Han}/u);
  });
});

/** The zh-TW catalog's values, read from the source so no key is missed. */
function zhValues(): Array<[string, string]> {
  const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
  const start = text.indexOf('\n  "zh-TW": {');
  const end = text.indexOf("\nfunction formatMessage");
  const values: Array<[string, string]> = [];
  for (const line of text.slice(start, end).split("\n")) {
    const match = /^\s+"([^"]+)":\s*"((?:[^"\\]|\\.)*)",?\s*$/.exec(line);
    if (match) values.push([match[1], match[2]]);
  }
  return values;
}

// Everywhere else the UI says 節點 and 工作站; a stray English "node" or
// "Station" in a Chinese sentence reads as untranslated. Placeholders such as
// {node} are not words the reader sees.
describe("zh-TW names the product's things in Chinese", () => {
  it("reads the whole zh-TW catalog", () => {
    expect(zhValues().length).toBeGreaterThan(2000);
  });

  it.each(["node", "Station"])("never says %s in a zh-TW sentence", (word) => {
    const hits = zhValues()
      .filter(([, value]) => new RegExp(`\\b${word}\\b`, "i").test(value.replace(/\{[^}]*\}/g, "")))
      .map(([key, value]) => `${key}: ${value}`);
    expect(hits).toEqual([]);
  });
});
