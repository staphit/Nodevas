import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Language } from "../../preferences";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { SignIn } from "../SignIn";
import { AppearanceEditor } from "./AppearanceEditor";

vi.mock("../SignIn", async () => {
  const actual = await vi.importActual<typeof import("../SignIn")>("../SignIn");
  return { ...actual, useViewer: () => null };
});

const setLanguage = (language: Language) =>
  useApp.setState({ preferences: { ...readPreferences(), language } });

const optionTexts = (select: HTMLElement) =>
  within(select)
    .getAllByRole("option")
    .map((option) => option.textContent);

// A language is listed in its own name, so a reader who landed in the wrong
// one can still find theirs: English mode said "Traditional Chinese".
describe("language picker", () => {
  beforeEach(() => setLanguage("en"));

  it.each<Language>(["en", "zh-TW"])("lists each language by its native name in %s mode (Settings)", (language) => {
    setLanguage(language);
    render(<AppearanceEditor />);
    const select = screen.getByRole("combobox", { name: language === "en" ? "Language" : "語系" });
    expect(optionTexts(select)).toEqual(["English", "繁體中文"]);
  });

  it.each<Language>(["en", "zh-TW"])("lists each language by its native name in %s mode (sign-in)", (language) => {
    setLanguage(language);
    render(<SignIn onSignedIn={vi.fn()} />);
    const select = screen.getByRole("combobox", { name: language === "en" ? "Language" : "語系" });
    expect(optionTexts(select)).toEqual(["English", "繁體中文"]);
  });

  it("marks each option with its language", () => {
    render(<AppearanceEditor />);
    const options = within(screen.getByRole("combobox", { name: "Language" })).getAllByRole("option");
    expect(options.map((option) => option.getAttribute("lang"))).toEqual(["en", "zh-TW"]);
  });
});
