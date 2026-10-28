import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { TOUR_CHAPTERS } from "./components/tour/chapters";

// Traditional Chinese text takes full-width punctuation (，；？！：) between
// CJK words. Half-width ,;?! next to a CJK character reads as a typo; code,
// placeholders like {n}, URLs and ASCII-only terms keep theirs, and so does a
// key name standing alone between spaces (「按 ? 鍵」).
const CJK = /[　-〿㐀-鿿＀-￯]/u;

function halfWidthNextToCJK(text: string): boolean {
  const at = (i: number) => text[i] ?? "";
  for (let i = 0; i < text.length; i += 1) {
    if (!",;?!".includes(text[i])) continue;
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

  it("uses full-width ，；？！ between CJK text in i18n.ts", () => {
    expect(offenders(zhValues())).toEqual([]);
  });

  it("uses full-width punctuation in the onboarding tour", () => {
    const steps = TOUR_CHAPTERS.flatMap((chapter) =>
      chapter.steps.flatMap((step): Array<[string, string]> => [
        [`${step.id}.title`, step.title],
        [`${step.id}.body`, step.body],
      ]),
    );
    expect(offenders(steps)).toEqual([]);
  });

  it("uses full-width punctuation in the tour's welcome card", () => {
    const lines = source("components/tour/OnboardingTour.tsx")
      .split("\n")
      .map((line, i): [string, string] => [`OnboardingTour.tsx:${i + 1}`, line])
      .filter(([, line]) => !/^\s*(\/\/|\/?\*|\{\/\*)/.test(line));
    expect(offenders(lines)).toEqual([]);
  });

  it("leaves ASCII punctuation outside CJK alone", () => {
    expect(halfWidthNextToCJK("{n} items, {m} done?")).toBe(false);
    expect(halfWidthNextToCJK("https://example.com/a?b=1")).toBe(false);
    expect(halfWidthNextToCJK("按 ? 鍵看說明")).toBe(false);
    expect(halfWidthNextToCJK("前端, 發布")).toBe(true);
    expect(halfWidthNextToCJK("（? 看說明）")).toBe(true);
  });
});
