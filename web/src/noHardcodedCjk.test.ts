/**
 * Every word the reader sees comes from the catalog in i18n.ts, so an English
 * interface never shows Chinese. This walks web/src with the TypeScript
 * scanner and fails on any string literal, template piece or JSX text that
 * holds Han characters or CJK punctuation outside i18n.ts. Comments and tests
 * are not looked at. The few literals that must stay (they read user input,
 * or are glyphs rather than words) are listed below with the reason.
 */

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";
import { SOURCE_SCAN_TIMEOUT } from "./sourceScan";

const SRC = join(__dirname);

/** Han, CJK symbols and punctuation, full-width forms. */
const CJK = /[　-〿㐀-鿿豈-﫿＀-￯]/u;

interface Allowed {
  file: string;
  /** Matched against the literal's text. */
  text: RegExp;
  reason: string;
}

const ALLOWED: Allowed[] = [
  {
    file: "components/CommandPalette.tsx",
    text: /./u,
    reason: "search keywords are bilingual on purpose: either language finds the command",
  },
  {
    file: "copilot/decisions.ts",
    text: /./u,
    reason: "words a reader types to allow or deny, matched against input, never shown",
  },
  {
    file: "components/SignIn.tsx",
    text: /^\/\[一-鿿\]\/$/u,
    reason: "regex that tells whether a server message is already in Chinese",
  },
  {
    file: "components/inspector/NodeMetaForm.tsx",
    text: /^\/\[,，\]\/$/u,
    reason: "regex that splits tags on either comma the reader types",
  },
  {
    file: "components/drawer/TableToolbar.tsx",
    text: /^\s*＋[↑↓←→]\s*$/u,
    reason: "add-row/column icon glyphs; the buttons carry translated titles",
  },
  {
    file: "components/menus/NodeCreateMenu.tsx",
    text: /^\s*＋\s*$/u,
    reason: "add icon glyph beside translated text",
  },
  {
    file: "components/inspector/NodeMetaForm.tsx",
    text: /^\s*＋\s*$/u,
    reason: "add icon glyph beside translated text",
  },
  {
    file: "components/menus/PlanMenu.tsx",
    text: /^\s*＋\s*$/u,
    reason: "add icon glyph beside translated text",
  },
  {
    file: "components/BoardToolbar.tsx",
    text: /^\s*＋\s*$/u,
    reason: "add icon glyph; the button carries a translated label",
  },
  {
    file: "components/timeline/TimelineCards.tsx",
    text: /^\s*＋\s*$/u,
    reason: "custom-plan marker glyph, aria-hidden",
  },
];

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      out.push(...sourceFiles(path));
    } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name) && !/\.d\.ts$/.test(name)) {
      out.push(path);
    }
  }
  return out;
}

const LITERAL_KINDS = new Set<ts.SyntaxKind>([
  ts.SyntaxKind.StringLiteral,
  ts.SyntaxKind.NoSubstitutionTemplateLiteral,
  ts.SyntaxKind.TemplateHead,
  ts.SyntaxKind.TemplateMiddle,
  ts.SyntaxKind.TemplateTail,
  ts.SyntaxKind.JsxText,
  ts.SyntaxKind.RegularExpressionLiteral,
]);

function cjkLiterals(path: string): { line: number; text: string }[] {
  const text = readFileSync(path, "utf8");
  // A literal with CJK in it means CJK in the file: most files have none, and
  // skipping their parse is most of this test's time.
  if (!CJK.test(text)) return [];
  const source = ts.createSourceFile(
    path,
    text,
    ts.ScriptTarget.Latest,
    true,
    path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const found: { line: number; text: string }[] = [];
  const visit = (node: ts.Node) => {
    if (LITERAL_KINDS.has(node.kind)) {
      const text = node.getText(source);
      if (CJK.test(text)) {
        found.push({ line: source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1, text });
      }
    }
    // JsxText is only reached through the children of a JSX element.
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

describe("no hard-coded Chinese outside the catalog", () => {
  it("keeps every CJK literal in i18n.ts or the allowlist", () => {
    const offenders: string[] = [];
    for (const path of sourceFiles(SRC)) {
      const file = relative(SRC, path).split(sep).join("/");
      if (file === "i18n.ts") continue;
      for (const { line, text } of cjkLiterals(path)) {
        const trimmed = text.trim();
        if (ALLOWED.some((entry) => entry.file === file && entry.text.test(trimmed))) continue;
        offenders.push(`${file}:${line} ${trimmed.slice(0, 80)}`);
      }
    }
    expect(offenders).toEqual([]);
    // It parses the whole tree synchronously: under a full parallel run that
    // is seconds, so the default 5 s timed out now and then.
  }, SOURCE_SCAN_TIMEOUT);

  it("allows nothing without a reason", () => {
    for (const entry of ALLOWED) expect(entry.reason.length).toBeGreaterThan(10);
  });
});
