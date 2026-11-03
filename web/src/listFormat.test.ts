import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { formatList } from "./i18n";
import { SOURCE_SCAN_TIMEOUT } from "./sourceScan";

// A list read out in Traditional Chinese is separated with 、, not the
// half-width ", " an English list takes.
describe("formatList", () => {
  it("joins with 、 in zh-TW", () => {
    expect(formatList(["claude", "codex", "gemini"], "zh-TW")).toBe("claude、codex、gemini");
  });

  it("joins with a comma in English", () => {
    expect(formatList(["claude", "codex"], "en")).toBe("claude, codex");
  });

  it("leaves one item and none alone", () => {
    expect(formatList(["only"], "zh-TW")).toBe("only");
    expect(formatList([], "zh-TW")).toBe("");
  });
});

// Lists shown to a reader go through formatList. These joins are data, not
// display: a tag field's editable text, front matter, React keys.
const NOT_DISPLAY = new Set([
  "components/inspector/NodeMetaForm.tsx",
  "explorer/frontMatter.ts",
]);

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return sources(path);
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : [];
  });
}

describe("lists shown to a reader", () => {
  it("are joined with formatList, not a hard-coded comma", () => {
    const root = resolve(process.cwd(), "src");
    const offenders = sources(root)
      .map((path) => relative(root, path).replaceAll("\\", "/"))
      .filter((path) => !NOT_DISPLAY.has(path))
      .flatMap((path) =>
        readFileSync(join(root, path), "utf8")
          .split("\n")
          .map((line, index) => ({ line, at: `${path}:${index + 1}` }))
          .filter(({ line }) => /\.join\(\s*["'`], ["'`]\s*\)/.test(line))
          .map(({ at, line }) => `${at}: ${line.trim()}`),
      );
    expect(offenders).toEqual([]);
  }, SOURCE_SCAN_TIMEOUT);
});
