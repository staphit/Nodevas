import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { translate } from "./i18n";
import { SOURCE_SCAN_TIMEOUT } from "./sourceScan";

// Traditional Chinese text takes full-width punctuation (，；？！：（）) between
// CJK words. Half-width ,;?!() next to a CJK character reads as a typo; code,
// placeholders like {n}, URLs and ASCII-only terms keep theirs, and so does a
// key name standing alone between spaces (「按 ? 鍵」).
const CJK = /[　-〿㐀-鿿＀-￯]/u;

function halfWidthNextToCJK(text: string): boolean {
  const at = (i: number) => text[i] ?? "";
  for (let i = 0; i < text.length; i += 1) {
    if (!",;?!()".includes(text[i])) continue;
    const spaceBefore = at(i - 1) === " ";
    const spaceAfter = at(i + 1) === " ";
    if (spaceBefore && spaceAfter) continue;
    const before = spaceBefore ? at(i - 2) : at(i - 1);
    const after = spaceAfter ? at(i + 2) : at(i + 1);
    if (CJK.test(before) || CJK.test(after)) return true;
  }
  return false;
}

const source = (path: string) => readFileSync(resolve(process.cwd(), "src", path), "utf8");

/** The zh-TW catalog's values, read from the source so no key is missed. */
function zhValues(): Array<[string, string]> {
  const text = source("i18n.ts");
  const start = text.indexOf('\n  "zh-TW": {');
  const end = text.indexOf("\nfunction formatMessage");
  const values: Array<[string, string]> = [];
  for (const line of text.slice(start, end).split("\n")) {
    const match = /^\s+"([^"]+)":\s*"((?:[^"\\]|\\.)*)",?\s*$/.exec(line);
    if (match) values.push([match[1], match[2]]);
  }
  return values;
}

const offenders = (entries: Array<[string, string]>) =>
  entries.filter(([, value]) => halfWidthNextToCJK(value)).map(([key, value]) => `${key}: ${value}`);

describe("zh-TW punctuation", () => {
  it("reads the whole zh-TW catalog", () => {
    expect(zhValues().length).toBeGreaterThan(2000);
  });

  it("uses full-width ，；？！（） between CJK text in i18n.ts", () => {
    expect(offenders(zhValues())).toEqual([]);
  });

  // 「所有 Agent (*)」: the bracket sits between Latin and a symbol, so the
  // neighbour check above never sees a CJK character, yet the line is Chinese.
  it("uses full-width （） anywhere in a zh-TW catalog value that has CJK text", () => {
    const hits = zhValues()
      .filter(([, value]) => CJK.test(value) && /[()]/.test(value))
      .map(([key, value]) => `${key}: ${value}`);
    expect(hits).toEqual([]);
  });

  it("leaves ASCII punctuation outside CJK alone", () => {
    expect(halfWidthNextToCJK("{n} items, {m} done?")).toBe(false);
    expect(halfWidthNextToCJK("https://example.com/a?b=1")).toBe(false);
    expect(halfWidthNextToCJK("按 ? 鍵看說明")).toBe(false);
    expect(halfWidthNextToCJK("前端, 發布")).toBe(true);
    expect(halfWidthNextToCJK("（? 看說明）")).toBe(true);
  });

  it("catches half-width parentheses next to CJK text", () => {
    expect(halfWidthNextToCJK("搜尋或命令 (Ctrl/⌘ K)")).toBe(true);
    expect(halfWidthNextToCJK("內容(Markdown 編輯)")).toBe(true);
    expect(halfWidthNextToCJK("(beta)版本")).toBe(true);
    expect(halfWidthNextToCJK("搜尋或命令（Ctrl/⌘ K）")).toBe(false);
    expect(halfWidthNextToCJK("Search (Ctrl/⌘ K)")).toBe(false);
    expect(halfWidthNextToCJK("{label} ({hint})")).toBe(false);
  });

  it("uses full-width （） for string literals with CJK text in the app's code", () => {
    const hits = sourceFiles().flatMap(({ path, text }) =>
      [...text.matchAll(/"([^"\n]*)"|`([^`]*)`|'([^'\n]*)'/g)]
        .map((match) => match[1] ?? match[2] ?? match[3] ?? "")
        .filter((value) => CJK.test(value) && halfWidthNextToCJK(value))
        .map((value) => `${path}: ${value}`),
    );
    expect(hits).toEqual([]);
  }, SOURCE_SCAN_TIMEOUT);

  // `${t("x")} (Ctrl K)` reads 「搜尋 (Ctrl K)」 in zh-TW: a hint is put
  // beside translated text through common.withHint, which picks （） there.
  it("never puts half-width () beside translated text composed in code", () => {
    const composed = [
      /\$?\{(?:t|translate)\((?:[^()]|\([^()]*\))*\)\}\s?\(/,
      /\)\s?\$\{(?:t|translate)\(/,
    ];
    const hits = sourceFiles().flatMap(({ path, text }) =>
      text
        .split("\n")
        .map((line, index) => ({ line, at: `${path}:${index + 1}` }))
        .filter(({ line }) => composed.some((pattern) => pattern.test(line)))
        .map(({ at, line }) => `${at}: ${line.trim()}`),
    );
    expect(hits).toEqual([]);
  }, SOURCE_SCAN_TIMEOUT);

  it("puts a hint beside a label with the reader's parentheses", () => {
    expect(translate("common.withHint", "en", { label: "Search", hint: "Ctrl/⌘ K" })).toBe("Search (Ctrl/⌘ K)");
    expect(translate("common.withHint", "zh-TW", { label: "搜尋", hint: "Ctrl/⌘ K" })).toBe("搜尋（Ctrl/⌘ K）");
  });
});

/** The app's own source, without tests and the message catalog itself. */
function sourceFiles(): Array<{ path: string; text: string }> {
  const root = resolve(process.cwd(), "src");
  const walk = (dir: string): string[] =>
    readdirSync(dir).flatMap((name) => {
      const path = join(dir, name);
      if (statSync(path).isDirectory()) return walk(path);
      return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
    });
  return walk(root)
    .map((path) => relative(root, path).replaceAll("\\", "/"))
    .filter((path) => path !== "i18n.ts")
    .map((path) => ({ path, text: readFileSync(join(root, path), "utf8") }));
}
