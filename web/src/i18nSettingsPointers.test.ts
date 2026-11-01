import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";
import type { Language } from "./preferences";

// Text that sends someone into Settings ("Settings → Agents → Link a new
// device", 「設定 → 這個專案」) names the section as its list shows it, in
// the reader's language, and each step after it as its row or button reads.
// A section renamed in one place and not the other leaves people looking
// for a heading that is not there.

const PAGES = ["general", "agents", "project", "account"] as const;
const web = process.cwd();
const repo = resolve(web, "..");

/** Every key of the catalog, read from the source so none is missed. */
function catalogKeys(): string[] {
  const text = readFileSync(resolve(web, "src", "i18n.ts"), "utf8");
  const keys = new Set<string>();
  for (const match of text.matchAll(/^\s+"([A-Za-z0-9_.-]+)":/gm)) keys.add(match[1]);
  return [...keys];
}

const separator = String.raw`\s*(?:→|->|›)\s*`;
// A step: anything up to the next separator or the end of the phrase, with
// one parenthesised part allowed ("Runner access token (Cloudflare Access)").
const step = String.raw`((?:[^→›\n,.:;"'」），。、()-]|-(?!>))+(?:\([^)\n]*\))?)`;

/** The steps of every Settings pointer in text: [["Agents", "Link a new device"], …]. */
export function settingsPointers(text: string, settings: string): string[][] {
  const head = new RegExp(`${settings}${separator}${step}((?:${separator}${step})*)`, "g");
  const found: string[][] = [];
  for (const match of text.matchAll(head)) {
    const rest = match[2]
      .split(new RegExp(separator))
      .map((part) => part.trim())
      .filter(Boolean);
    found.push([match[1].trim(), ...rest]);
  }
  return found;
}

function badPointers(language: Language, texts: Array<[string, string]>): string[] {
  const settings = translate("mode.settings", language);
  const pages = new Set(PAGES.map((id) => translate(`settingsPage.page.${id}`, language)));
  const labels = new Set(
    catalogKeys()
      .filter((key) => hasMessage(language, key))
      .map((key) => translate(key, language)),
  );
  const bad: string[] = [];
  for (const [where, text] of texts) {
    for (const [page, ...steps] of settingsPointers(text, settings)) {
      if (!pages.has(page)) bad.push(`${where}: "${page}" is not a Settings section (${[...pages].join(", ")})`);
      for (const label of steps) {
        if (!labels.has(label)) bad.push(`${where}: "${label}" is not a label the app shows`);
      }
    }
  }
  return bad;
}

const catalog = (language: Language): Array<[string, string]> =>
  catalogKeys()
    .filter((key) => hasMessage(language, key))
    .map((key) => [key, translate(key, language)]);

function sources(): Array<[string, string]> {
  const out: Array<[string, string]> = [["scripts/install.sh", readFileSync(join(repo, "scripts", "install.sh"), "utf8")]];
  const cli = join(repo, "cmd", "nodevas");
  for (const name of readdirSync(cli)) {
    if (name.endsWith(".go") && !name.endsWith("_test.go")) {
      out.push([`cmd/nodevas/${name}`, readFileSync(join(cli, name), "utf8")]);
    }
  }
  return out;
}

describe("pointers into Settings", () => {
  it("are found with every step", () => {
    expect(settingsPointers("in Settings → General → Voice copilot.", "Settings")).toEqual([
      ["General", "Voice copilot"],
    ]);
    expect(settingsPointers("(hub: Settings -> Agents -> Link a new device)", "Settings")).toEqual([
      ["Agents", "Link a new device"],
    ]);
    expect(
      settingsPointers("(Settings -> Agents -> Advanced -> Runner access token (Cloudflare Access), as", "Settings"),
    ).toEqual([["Agents", "Advanced", "Runner access token (Cloudflare Access)"]]);
    expect(settingsPointers("在「設定 → 這個專案」管理", "設定")).toEqual([["這個專案"]]);
  });

  it.each(["en", "zh-TW"] as const)("in the %s catalog name real sections and labels", (language) => {
    expect(badPointers(language, catalog(language))).toEqual([]);
  });

  it("in the installer and the CLI name real sections and labels", () => {
    expect(badPointers("en", sources())).toEqual([]);
  });
});
