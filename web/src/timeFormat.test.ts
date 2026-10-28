import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { formatLocalizedDateTime, formatLocalizedTime } from "./i18n";
import { formatClock } from "./stepup/StepUpDialog";

// One moment read two ways — 上午02:42 in the timeline, 凌晨2:42 in a run's
// details — looks like two moments. Every time of day goes through one helper
// with one set of options, so a time alone is the time part of a date-time.

const night = new Date(2026, 9, 29, 2, 42);
const afternoon = new Date(2026, 9, 29, 14, 5);

describe("time of day", () => {
  for (const language of ["zh-TW", "en"] as const) {
    it(`reads the same alone and in a date-time (${language})`, () => {
      for (const at of [night, afternoon]) {
        const time = formatLocalizedTime(at, language);
        expect(time).not.toBe("");
        expect(formatLocalizedDateTime(at, language).endsWith(time)).toBe(true);
      }
    });
  }

  it("does not pad the hour in zh-TW", () => {
    expect(formatLocalizedTime(night, "zh-TW")).not.toMatch(/0\d:/);
    expect(formatLocalizedTime(night, "zh-TW")).toMatch(/2:42$/);
  });

  it("takes an ISO string as well as a Date", () => {
    expect(formatLocalizedTime(afternoon.toISOString(), "en")).toBe(formatLocalizedTime(afternoon, "en"));
    expect(formatLocalizedTime("nonsense", "en")).toBe("");
  });

  it("gives the step-up expiry the same clock as everywhere else", () => {
    const iso = afternoon.toISOString();
    expect(formatClock(iso)).toBe(formatLocalizedTime(iso));
    expect(formatClock(undefined)).toBe("");
  });

  it("is formatted nowhere else with its own options", () => {
    const root = resolve(process.cwd(), "src");
    const walk = (dir: string): string[] =>
      readdirSync(dir).flatMap((name) => {
        const path = join(dir, name);
        if (statSync(path).isDirectory()) return walk(path);
        return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
      });
    const offenders = walk(root)
      .map((path) => relative(root, path).replaceAll("\\", "/"))
      .flatMap((path) =>
        readFileSync(join(root, path), "utf8")
          .split("\n")
          .map((line, index) => ({ line, at: `${path}:${index + 1}` }))
          .filter(({ line }) => /\bhour:\s*["'`]|timeStyle:|toLocaleTimeString|hourCycle/.test(line))
          .map(({ at, line }) => `${at}: ${line.trim()}`),
      );
    // The one place: the shared options in i18n.ts.
    expect(offenders).toHaveLength(1);
    expect(offenders[0]).toMatch(/^i18n\.ts:/);
  });
});
