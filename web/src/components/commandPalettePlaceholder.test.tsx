import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { Language } from "../preferences";
import { readPreferences } from "../preferences";
import { TEXT_SCALE } from "../preferences/schema";
import { useApp } from "../store";
import { CommandPalette } from "./CommandPalette";
import { openCommandPalette } from "./topbar/actions";

// jsdom lays nothing out, so the placeholder is measured with a deliberately
// generous model: a CJK or full-width character is 1em, anything else 0.5em
// (the UI font averages about 0.43em on Latin text). The input's text box is
// 572px wide in a 1280px window: 680px palette less borders, padding, the
// search glyph and the gaps (10a-canvas-tools.css).
const INPUT_TEXT_WIDTH = 572;
const INPUT_FONT_PX = 15;

function estimatedWidth(text: string, fontPx: number): number {
  let ems = 0;
  for (const char of text) {
    ems += /[⺀-鿿豈-﫿＀-￯　-〿]/u.test(char) ? 1 : 0.5;
  }
  return ems * fontPx;
}

function openIn(language: Language) {
  useApp.setState({ preferences: { ...readPreferences(), language } });
  render(<CommandPalette />);
  act(() => openCommandPalette());
  return screen.getByRole("textbox") as HTMLInputElement;
}

afterEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("command palette search field", () => {
  it.each(["en", "zh-TW"] as const)("shows its whole placeholder at the largest text size (%s)", (language) => {
    const input = openIn(language);
    const width = estimatedWidth(input.placeholder, INPUT_FONT_PX * TEXT_SCALE.max);
    expect(width, input.placeholder).toBeLessThanOrEqual(INPUT_TEXT_WIDTH);
  });

  it("names everything it searches, and shows that as a tooltip", () => {
    const input = openIn("en");
    const name = input.getAttribute("aria-label") ?? "";
    expect(name).toMatch(/projects.*nodes.*Markdown.*command/i);
    expect(input.title).toBe(name);
  });

  it("names everything it searches in Traditional Chinese too", () => {
    const input = openIn("zh-TW");
    const name = input.getAttribute("aria-label") ?? "";
    expect(name).toMatch(/專案.*節點.*Markdown.*命令/);
    expect(input.title).toBe(name);
  });
});
