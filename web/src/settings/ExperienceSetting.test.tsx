import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { translate } from "../i18n";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { EditionProvider } from "../runners/edition";
import { useApp } from "../store";
import { ExperienceSetting } from "./ExperienceSetting";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function start(stored: Record<string, string> = {}, language: "en" | "zh-TW" = "en") {
  configurePreferenceStorage(fakeStorage(stored));
  useApp.setState({ preferences: { ...readPreferences(), language } });
}

function single() {
  return render(
    <EditionProvider edition="single">
      <ExperienceSetting />
    </EditionProvider>,
  );
}

const en = (key: string) => translate(key, "en");

beforeEach(() => start());

describe("ExperienceSetting", () => {
  it("renders nothing on a team hub", () => {
    start({ "vised-experience": "simple" });
    const { container } = render(<ExperienceSetting />);
    expect(container).toBeEmptyDOMElement();
  });

  it("offers the two ways, Everything chosen by default", () => {
    single();
    expect(screen.getByRole("group", { name: en("simple.setting.title") })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: new RegExp(en("simple.setting.full")) })).toBeChecked();
    expect(screen.getByRole("radio", { name: new RegExp(en("simple.setting.simple")) })).not.toBeChecked();
    expect(screen.getByText(en("simple.setting.simpleWhat"))).toBeInTheDocument();
    expect(screen.getByText(en("simple.setting.fullWhat"))).toBeInTheDocument();
  });

  it("Simple writes simple", () => {
    single();
    fireEvent.click(screen.getByRole("radio", { name: new RegExp(en("simple.setting.simple")) }));
    expect(useApp.getState().preferences.experience).toBe("simple");
    expect(screen.queryByText(en("simple.setting.leftNote"))).toBeNull();
  });

  it("Everything writes full and says finished work still stays here", () => {
    start({ "vised-experience": "simple" });
    single();
    expect(screen.queryByText(en("simple.setting.leftNote"))).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: new RegExp(en("simple.setting.full")) }));
    expect(useApp.getState().preferences.experience).toBe("full");
    expect(screen.getByRole("status")).toHaveTextContent(en("simple.setting.leftNote"));
  });

  it("speaks Traditional Chinese for a zh-TW reader", () => {
    start({}, "zh-TW");
    single();
    expect(screen.getByRole("group", { name: translate("simple.setting.title", "zh-TW") })).toBeInTheDocument();
    expect(screen.getByText(translate("simple.setting.simpleWhat", "zh-TW"))).toBeInTheDocument();
  });
});
