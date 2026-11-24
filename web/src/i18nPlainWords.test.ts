import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";
import {
  FOLLOW_UP_ERROR_KEYS,
  groupSkipReason,
  SIMPLE_FOLLOW_UP_ERROR_KEYS,
  SIMPLE_GROUP_SKIP_KEYS,
  SIMPLE_GROUP_SKIP_OTHER,
} from "./runs/dialog/followUpErrors";

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
  "runDialog.simple.",
  "picker.simple.",
  "busy.simple.",
  "overlap.simple.",
];
const SINGLE_KEYS = ["start.describeHeading", "lane.emptyAskAgent", "dispatch.simpleNoChecksLocal"];

const EN_WORDS = /\b(repo|repository|branch|merge|PR|pull request|CLI|worktree|commit|git|terminal|runner|tokens?|CI|ACP|diff)\b/i;
const ZH_WORDS = /儲存庫|分支|合併|工作樹|提交|終端機|拉取請求|runner|token|\bCI\b|ACP|差異/i;

/** Allowed to use one of the words, and why. */
const ALLOWED: Record<string, string> = {
  "local.firstFolder.gitNeeded": "it names Git, the program the person has to install",
  "local.scan.signInHint": "it names Terminal, the app the person opens to sign an agent in",
  "result.signedOut": "it names Terminal, the app the person opens to sign an agent in",
  "simple.local.cliHint": "it names Terminal, the app the commands are typed in",
};

/** The keys allowed only to name the Terminal app (what macOS and Windows
 * call it), as the app to open and nothing else. */
const NAMES_TERMINAL = ["local.scan.signInHint", "result.signedOut", "simple.local.cliHint"];

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

  it("names the Terminal app only as the app to open, with nothing else a developer says", () => {
    for (const key of NAMES_TERMINAL) {
      expect(keys).toContain(key);
      const en = withoutPlaceholders(translate(key, "en"));
      const zh = withoutPlaceholders(translate(key, "zh-TW"));
      expect(en).toContain("the Terminal app");
      expect(zh).toContain("「終端機」");
      expect(EN_WORDS.test(en.replace("the Terminal app", ""))).toBe(false);
      expect(ZH_WORDS.test(zh.replace("「終端機」", ""))).toBe(false);
    }
  });

  // One name for the daily cap: the HUD, the notification settings and the
  // toast all say "today's limit", never a budget or spending (it can be a
  // token cap, not money).
  it("calls the daily cap today's limit everywhere", () => {
    const named = ["simple.energy.today", "simple.notify.default", "simple.notify.granted", "simple.notify.limitReached"];
    for (const key of named) {
      expect(translate(key, "en")).toMatch(/today's limit/i);
      expect(translate(key, "zh-TW")).toContain("今天的額度");
    }
    for (const key of [...named, "simple.notify.limitBody"]) {
      expect(translate(key, "en")).not.toMatch(/budget|spending|UTC/i);
      expect(translate(key, "zh-TW")).not.toMatch(/預算|花費|UTC/);
    }
  });

  // Notifications come from the open page while it is hidden; a closed one
  // sends none, and neither language says otherwise.
  it("promises notifications only while the page is hidden, not closed", () => {
    for (const key of ["simple.notify.default", "simple.notify.granted"]) {
      expect(translate(key, "en")).toContain("hidden");
      expect(translate(key, "en")).toMatch(/closing the page stops/i);
      expect(translate(key, "zh-TW")).toContain("隱藏");
      expect(translate(key, "zh-TW")).toContain("關掉頁面");
      expect(translate(key, "zh-TW")).not.toContain("沒開著");
    }
  });

  // The toggle's hint points at OpenCode's own setup, not at the toggle.
  it("says OpenCode needs its own setup to ask", () => {
    expect(translate("simple.local.permissionRelayHelp", "en")).toMatch(/OpenCode itself is set up/);
    expect(translate("simple.local.permissionRelayHelp", "zh-TW")).not.toContain("這項設定");
    expect(translate("simple.local.permissionRelayHelp", "zh-TW")).toContain("另外設定");
  });

  // A refused follow-up in Simple says one of these families' sentences,
  // never runDialog.err.* (which says runner, branch, pull request).
  it("has a plain sentence for every follow-up refusal Simple can meet", () => {
    expect(Object.keys(SIMPLE_FOLLOW_UP_ERROR_KEYS).sort()).toEqual(Object.keys(FOLLOW_UP_ERROR_KEYS).sort());
    for (const key of Object.values(SIMPLE_FOLLOW_UP_ERROR_KEYS)) {
      expect(plain(key), key).toBe(true);
      expect(hasMessage("en", key) && hasMessage("zh-TW", key), key).toBe(true);
    }
    for (const key of ["runDialog.simple.resumeLost", "runDialog.simple.ph.shownAbove", "runDialog.simple.shownAbove"]) {
      expect(keys).toContain(key);
    }
  });

  // A group member the hub skipped reads as a plain sentence in Simple, for
  // every code groupFollowUpSkipCode can return and any it adds later.
  it("has a plain sentence for every group skip Simple can meet, never a raw code", () => {
    const hubCodes = ["BUDGET_EXCEEDED", "FAILED_PRECONDITION", "NOT_FOUND", "INVALID_ARGUMENT"];
    for (const code of hubCodes) expect(Object.keys(SIMPLE_GROUP_SKIP_KEYS)).toContain(code);
    for (const key of [...Object.values(SIMPLE_GROUP_SKIP_KEYS), SIMPLE_GROUP_SKIP_OTHER]) {
      expect(plain(key), key).toBe(true);
      expect(hasMessage("en", key) && hasMessage("zh-TW", key), key).toBe(true);
    }
    for (const language of ["en", "zh-TW"] as const) {
      const t = (key: string, values?: Record<string, string | number>) => translate(key, language, values);
      for (const code of [...hubCodes, "SOMETHING_NEW", ...Object.keys(SIMPLE_FOLLOW_UP_ERROR_KEYS)]) {
        const reason = groupSkipReason(code, t, true);
        expect(reason, code).not.toContain(code);
        expect(reason, code).not.toMatch(/\b[A-Z]+_[A-Z_]+\b/);
      }
    }
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
