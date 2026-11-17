import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";

// There is no global en/zh-TW parity check, and translate() falls back to
// English, so a key missing from one catalog ships half-translated without
// a sound. The copilot's and its drafts' keys are held to both catalogs.
const FAMILIES = ["copilot.", "draft.", "undo.label."];

/** Keys of one catalog, read from the source so none is missed. */
function catalogKeys(language: "en" | "zh-TW"): string[] {
  const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
  const zhStart = text.indexOf('\n  "zh-TW": {');
  const end = text.indexOf("\nfunction formatMessage");
  const enStart = text.indexOf("\n  en: {");
  const section = language === "en" ? text.slice(enStart, zhStart) : text.slice(zhStart, end);
  const keys: string[] = [];
  for (const line of section.split("\n")) {
    const match = /^\s{4}"([^"]+)":/.exec(line);
    if (match) keys.push(match[1]);
  }
  return keys;
}

const inFamily = (key: string) => FAMILIES.some((family) => key.startsWith(family));

describe("copilot and draft keys in both catalogs", () => {
  it("reads both catalogs", () => {
    expect(catalogKeys("en").length).toBeGreaterThan(2000);
    expect(catalogKeys("zh-TW").length).toBeGreaterThan(2000);
  });

  it("has every en key in zh-TW and the other way round", () => {
    const en = catalogKeys("en").filter(inFamily);
    const zh = catalogKeys("zh-TW").filter(inFamily);
    expect(en.filter((key) => !hasMessage("zh-TW", key))).toEqual([]);
    expect(zh.filter((key) => !hasMessage("en", key))).toEqual([]);
    expect(en.length).toBeGreaterThan(100);
  });

  it("reads the draft keys in Chinese, with 節點 for node", () => {
    expect(translate("draft.title", "zh-TW", { n: 8, e: 7 })).toBe("草稿：8 個節點、7 條依賴");
    expect(translate("draft.create", "zh-TW", { n: 8 })).toBe("建立 8 個節點");
    expect(translate("draft.created", "zh-TW", { n: 8 })).toBe("已建立 8 個節點");
    const zh = catalogKeys("zh-TW").filter((key) => key.startsWith("draft.") || key.startsWith("copilot."));
    const english = zh.filter((key) => /\bnodes?\b/i.test(translate(key, "zh-TW")));
    expect(english).toEqual([]);
  });
});
