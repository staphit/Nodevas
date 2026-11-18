// Settings › General › Notifications. Simple (single-user edition) says
// what it tells you about in plain words: no red CI, no merge results, which
// a Simple run never has. Full mode and a team hub keep the precise list.

import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { readPreferences, type Language } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useApp } from "../store";
import { RunNotifySettings } from "./RunToasts";

function show(permission: NotificationPermission, edition: Edition, experience: "simple" | "full", language: Language = "en") {
  vi.stubGlobal("Notification", { permission, requestPermission: vi.fn() });
  useApp.setState({ preferences: { ...readPreferences(), language, experience } });
  return render(
    <EditionProvider edition={edition}>
      <RunNotifySettings />
    </EditionProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("RunNotifySettings", () => {
  it.each([
    ["default", "en"],
    ["granted", "en"],
    ["denied", "en"],
    ["default", "zh-TW"],
    ["granted", "zh-TW"],
    ["denied", "zh-TW"],
  ] as const)("says it in plain words in Simple (%s, %s)", (permission, language) => {
    const { container } = show(permission, "single", "simple", language);
    const text = container.querySelector("p")?.textContent ?? "";
    expect(text).not.toMatch(/\bCI\b|merge|合併|\brun\b|執行提示/i);
    expect(text).toMatch(/agent|notif|通知|提示/i);
  });

  it.each([
    ["single", "full"],
    ["team", "simple"],
  ] as const)("keeps the precise list in %s with %s", (edition, experience) => {
    const { container } = show("default", edition, experience);
    expect(container.querySelector("p")).toHaveTextContent(/red CI and merge results/);
  });
});
