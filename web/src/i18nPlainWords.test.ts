import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";

// Easy start speaks to someone who does not write code: the first-run
// questions, the agent scan and install help, adding an agent by hand, the
// first folder, the describe box, the result buttons and the Simple setting
// never use a developer's word in either language. The technical panels keep
// theirs; these families are the plain path only.

const FAMILIES = [
  "firstRun.",
  "local.scan.",
  "local.install.",
  "local.addAgent.",
  "local.firstFolder.",
  "local.pickFolder.",
  "globalAgents.simple.",
  "result.",
  "simple.",
];
const SINGLE_KEYS = ["start.describeHeading", "lane.emptyAskAgent", "dispatch.simpleNoChecksLocal"];

const EN_WORDS = /\b(repo|repository|branch|merge|PR|pull request|CLI|worktree|commit|git|terminal|runner|tokens?|CI|ACP|diff)\b/i;
const ZH_WORDS = /儲存庫|分支|合併|工作樹|提交|終端機|拉取請求|runner|token|\bCI\b|ACP|差異/i;

/** Allowed to use one of the words, and why. */
const ALLOWED: Record<string, string> = {
  "local.firstFolder.gitNeeded": "it names Git, the program the person has to install",
};

/** Every key of one catalog, read from the source so none is missed. */
function catalogKeys(language: "en" | "zh-TW"): string[] {
  const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
  const zhStart = text.indexOf('\n  "zh-TW": {');
  const part = language === "en" ? text.slice(0, zhStart) : text.slice(zhStart, text.indexOf("\nfunction formatMessage"));
  return [...part.matchAll(/^\s+"([A-Za-z0-9_.-]+)":/gm)].map((match) => match[1]);
}

const plain = (key: string) => SINGLE_KEYS.includes(key) || FAMILIES.some((family) => key.startsWith(family));
const keys = [...new Set([...catalogKeys("en"), ...catalogKeys("zh-TW")].filter(plain))];
const withoutPlaceholders = (value: string) => value.replace(/\{[^}]*\}/g, "");

describe("easy start's plain words", () => {
  it("covers every family", () => {
    expect(keys.length).toBeGreaterThan(120);
    for (const family of FAMILIES) expect(keys.some((key) => key.startsWith(family))).toBe(true);
    for (const key of SINGLE_KEYS) expect(keys).toContain(key);
  });

  it("are in both catalogs", () => {
    expect(keys.filter((key) => !hasMessage("en", key) || !hasMessage("zh-TW", key))).toEqual([]);
  });

  it("read in Chinese in the Chinese catalog", () => {
    expect(keys.filter((key) => !/\p{Script=Han}/u.test(translate(key, "zh-TW")))).toEqual([]);
  });

  it("never use a developer's word in English", () => {
    const hits = keys
      .filter((key) => !(key in ALLOWED))
      .filter((key) => EN_WORDS.test(withoutPlaceholders(translate(key, "en"))))
      .map((key) => `${key}: ${translate(key, "en")}`);
    expect(hits).toEqual([]);
  });

  it("never use a developer's word in Chinese", () => {
    const hits = keys
      .filter((key) => !(key in ALLOWED))
      .filter((key) => ZH_WORDS.test(withoutPlaceholders(translate(key, "zh-TW"))))
      .map((key) => `${key}: ${translate(key, "zh-TW")}`);
    expect(hits).toEqual([]);
  });

  it("allows only keys that exist", () => {
    for (const key of Object.keys(ALLOWED)) expect(hasMessage("en", key)).toBe(true);
  });

  it("checks the words it says it checks", () => {
    expect(EN_WORDS.test("Merge locally")).toBe(true);
    expect(EN_WORDS.test("Open a PR")).toBe(true);
    expect(EN_WORDS.test("Merged into it")).toBe(false);
    expect(ZH_WORDS.test("合併到本機")).toBe(true);
    expect(ZH_WORDS.test("套用到你的資料夾")).toBe(false);
    // Agent activity, notifications and the run view's summary say none of
    // these in Simple either.
    expect(EN_WORDS.test("Tokens today 12k tokens")).toBe(true);
    expect(EN_WORDS.test("red CI and merge results")).toBe(true);
    expect(EN_WORDS.test("OpenCode in ACP mode")).toBe(true);
    expect(EN_WORDS.test("Show the diff")).toBe(true);
    expect(EN_WORDS.test("Show the full record")).toBe(false);
    expect(ZH_WORDS.test("今日 token")).toBe(true);
    expect(ZH_WORDS.test("CI 變紅和合併結果")).toBe(true);
    expect(ZH_WORDS.test("ACP 模式的 OpenCode")).toBe(true);
    expect(ZH_WORDS.test("顯示詳細紀錄")).toBe(false);
  });
});
