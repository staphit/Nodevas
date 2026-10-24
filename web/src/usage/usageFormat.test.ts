import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { readPreferences } from "../preferences";
import { useApp } from "../store";
import { niceScale } from "./UsageChart";
import { formatDayKey, formatShare, formatTokens, formatUSD, perDay } from "./usageFormat";

beforeEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("usage formatting", () => {
  it("shortens token counts", () => {
    expect(formatTokens(0)).toBe("0");
    expect(formatTokens(812)).toBe("812");
    expect(formatTokens(34_500)).toBe("34.5K");
    expect(formatTokens(1_234_567)).toBe("1.2M");
  });

  it("never shows a non-zero cost as zero", () => {
    expect(formatUSD(0)).toBe("$0.00");
    expect(formatUSD(0.004)).toBe("<$0.01");
    expect(formatUSD(0.01)).toBe("$0.01");
    expect(formatUSD(1234.567)).toBe("$1,234.57");
    expect(formatUSD(Number.NaN)).toBe("$0.00");
  });

  it("gives shares and daily averages", () => {
    expect(formatShare(1, 3)).toBe("33%");
    expect(formatShare(1, 1000)).toBe("<1%");
    expect(formatShare(0, 10)).toBe("0%");
    expect(formatShare(5, 0)).toBe("");
    expect(perDay(70, 7)).toBe(10);
    expect(perDay(5, 0)).toBe(0);
  });

  it("reads a day key as a UTC date", () => {
    expect(formatDayKey("2026-10-24")).toBe("Oct 24");
    expect(formatDayKey("nope")).toBe("nope");
  });

  it("rounds the chart axis up to a round step", () => {
    expect(niceScale(0)).toEqual({ max: 1, step: 1 / 3 });
    expect(niceScale(1234)).toEqual({ max: 1500, step: 500 });
    expect(niceScale(9)).toEqual({ max: 10, step: 5 });
    const { max, step } = niceScale(0.42);
    expect(max).toBeGreaterThanOrEqual(0.42);
    expect(max / step).toBeLessThanOrEqual(4);
  });
});

describe("usage strings", () => {
  // Every usage key is in both catalogs: zh-TW falls back to English
  // silently, so a missing one would never fail a render test.
  it("has every key in English and Traditional Chinese", () => {
    const source = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
    const keys = [...source.matchAll(/^\s+"((?:usage\.|topbar\.usage)[\w.]*)":/gm)].map((m) => m[1]);
    expect(keys.length).toBeGreaterThan(50);
    const counts = new Map<string, number>();
    for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1);
    const odd = [...counts].filter(([, n]) => n !== 2).map(([key]) => key);
    expect(odd).toEqual([]);
  });
});
