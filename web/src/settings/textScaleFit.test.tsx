import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { PAGE_FORMATS } from "../components/drawer/pageFormats";
import { translate } from "../i18n";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useRunners } from "../runners/runnerStore";
import { useApp } from "../store";
import { openSettings } from "./openSettings";
import SettingsView from "./SettingsView";

// At 150% text the Settings search box showed "Search settings: bud" and the
// drawer's page-format select "Markdown (.m": a fixed-width box with text
// that grows. jsdom lays nothing out, so this estimates text widths on the
// generous side and reads the boxes' sizes from the stylesheets.

vi.mock("../components/SignIn", () => ({
  useViewer: () => null,
  useTeamServer: () => false,
  useSignOut: () => () => {},
}));

const MAX_SCALE = 1.5;

/** A generous width estimate in em: wider than the app's sans-serif renders. */
function estimateEm(text: string): number {
  let em = 0;
  for (const char of text) {
    if (/[　-鿿＀-￯…]/u.test(char)) em += 1;
    else if (/[mwMW]/.test(char)) em += 0.9;
    else if (/[A-Z]/.test(char)) em += 0.72;
    else if (/[a-z0-9]/.test(char)) em += 0.6;
    else em += 0.35;
  }
  return em;
}

function css(file: string): string {
  return readFileSync(resolve(process.cwd(), "src/styles", file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
}

function rules(text: string, selector: string): string {
  const blocks: string[] = [];
  const pattern = /([^{}]+)\{([^}]*)\}/g;
  for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
    if (match[1].split(",").map((part) => part.trim()).includes(selector)) blocks.push(match[2]);
  }
  return blocks.join("\n");
}

/** A length declared as `Npx` or `calc(Npx * var(--font-scale, 1))`, at a text scale. */
function lengthAt(block: string, property: string, scale: number): number | null {
  const match = new RegExp(`(?:^|[;\\s])${property}:\\s*([^;]+);`).exec(block);
  if (!match) return null;
  const value = match[1].trim();
  const scaled = /^calc\((\d+(?:\.\d+)?)px \* var\(--font-scale, 1\)\)$/.exec(value);
  if (scaled) return Number(scaled[1]) * scale;
  const fixed = /^(\d+(?:\.\d+)?)px$/.exec(value);
  return fixed ? Number(fixed[1]) : null;
}

function fakeStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
  useRunners.setState({ loaded: false, loading: false, runners: [], error: null, creating: false, revoking: {} });
  vi.spyOn(api, "listRunners").mockResolvedValue({ runners: [] });
  vi.spyOn(api, "getStepUp").mockResolvedValue({ required: false, active: false });
});

afterEach(() => vi.restoreAllMocks());

describe("Settings search at 150% text", () => {
  // The nav column is 240px; less its 12px padding each side, the box's
  // 10px padding each side and its border, the text gets 192px.
  const navColumn = 240;
  const textRoom = navColumn - 2 * 12 - 2 * 10 - 2;

  it("keeps the nav column at 240px, the room the placeholder is measured against", () => {
    expect(rules(css("25-station.css"), ".settings-page")).toMatch(/grid-template-columns:\s*240px minmax\(0, 1fr\)/);
    expect(rules(css("27-settings.css"), ".settings-search")).toMatch(/padding:\s*6px 10px;/);
  });

  for (const language of ["en", "zh-TW"] as const) {
    it(`shows the whole placeholder in ${language}`, () => {
      useApp.getState().updateUIPreference("language", language);
      act(() => openSettings("appearance"));
      render(<SettingsView />);
      const search = screen.getByRole("searchbox");
      const fontSize = 13 * MAX_SCALE;
      const placeholder = search.getAttribute("placeholder") ?? "";
      expect(placeholder).not.toBe("");
      expect(estimateEm(placeholder) * fontSize).toBeLessThanOrEqual(textRoom);
      // What the search finds is still said, on hover.
      expect(search.getAttribute("title") ?? "").toMatch(language === "en" ? /budget/ : /預算/);
    });
  }
});

describe("Page-format select at 150% text", () => {
  // Arrow, padding and border beside the label.
  const chrome = 30;
  const editor = css("06-editor.css");

  for (const language of ["en", "zh-TW"] as const) {
    it(`fits every format's label in ${language}`, () => {
      for (const selector of [".content-page-format-select", ".content-page-create select"]) {
        const block = rules(editor, selector);
        const fontSize = lengthAt(block, "font-size", MAX_SCALE) ?? 0;
        expect(fontSize, selector).toBeGreaterThan(0);
        const widest = Math.max(
          ...PAGE_FORMATS.map(
            ([format, , extension]) => estimateEm(`${translate(`format.${format}`, language)} (${extension})`) * fontSize,
          ),
        );
        const maxWidth = lengthAt(block, "max-width", MAX_SCALE);
        if (maxWidth !== null) expect(widest + chrome, selector).toBeLessThanOrEqual(maxWidth);
        // A fixed height cuts off text that grows; a minimum does not.
        expect(block, selector).not.toMatch(/(?:^|[;\s])height:\s*\d+px/);
      }
    });
  }
});
