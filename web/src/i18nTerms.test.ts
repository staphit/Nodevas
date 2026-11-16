import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";

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
  "station.tab.project",
  "globalAgents.form.task",
  "globalAgents.col.status",
  "globalAgents.action.workspace",
  "local.title",
  "local.agentsFound",
  "local.repos",
  "local.permission.read",
  "local.permission.edit",
  "local.permission.full",
  "local.askEveryRun",
  "local.permissionRelay",
  "offerAsk.title",
  "offerAsk.start",
  "offerAsk.decline",
  "runs.status.asking",
  "repoAdd.button",
  "repoAdd.title",
  "repoAdd.initOffer",
  "repoAdd.testCommand",
  "prefsSync.local",
  "prefsSync.account",
  "prefsSync.deviceOnly",
  "prefsSync.offline",
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

// Global agents (phase 2): every key the English catalog has is in the
// Traditional Chinese one too, so no line falls back to English there.
describe("global agents keys", () => {
  it("are in both catalogs", () => {
    const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
    const en = text.slice(0, text.indexOf('\n  "zh-TW": {'));
    const keys = [...en.matchAll(/^\s+"((?:globalAgents|station\.tab)[^"]*)":/gm)].map((match) => match[1]);
    expect(keys.length).toBeGreaterThan(80);
    expect(keys.filter((key) => !hasMessage("zh-TW", key))).toEqual([]);
    expect(hasMessage("en", "game.action.globalAgents") && hasMessage("zh-TW", "game.action.globalAgents")).toBe(true);
    expect(hasMessage("zh-TW", "topbar.cmd.openGlobalAgents")).toBe(true);
  });
});

// Single-user edition (phase S): every This computer, add-repo, ask and
// result-button key is in both catalogs, the Chinese one reads in Chinese,
// and none of them says "runner" to a single-user reader in either language
// (the team-only lines name Nodevas Runner, the separate program, on purpose).
describe("single-user edition keys", () => {
  const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
  const en = text.slice(0, text.indexOf('\n  "zh-TW": {'));
  const keys = [
    ...en.matchAll(/^\s+"((?:local|offerAsk|repoAdd|result)\.[^"]*|(?:runs|dispatch|terminal|globalAgents)\.[^"]+Local)":/gm),
  ].map((match) => match[1]);
  const teamOnly = new Set([
    "repoAdd.onRunner",
    "repoAdd.openOnRunner",
    "repoAdd.advancedCli",
    "offerAsk.waitingRemote",
    "offerAsk.waitingOn",
    "offerAsk.error.approve_on_runner",
  ]);

  it("are in both catalogs and read in Chinese", () => {
    expect(keys.length).toBeGreaterThan(100);
    expect(keys.filter((key) => !hasMessage("zh-TW", key))).toEqual([]);
    expect(keys.filter((key) => !/\p{Script=Han}/u.test(translate(key, "zh-TW")))).toEqual([]);
  });

  it("never say runner to a single-user reader", () => {
    const said = keys
      .filter((key) => !teamOnly.has(key))
      .flatMap((key) => [translate(key, "en"), translate(key, "zh-TW")].map((value) => `${key}: ${value}`))
      .filter((line) => /runner/i.test(line.slice(line.indexOf(": ") + 2)));
    expect(said).toEqual([]);
  });
});
