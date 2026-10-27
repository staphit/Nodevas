import { afterEach, describe, expect, it } from "vitest";

import { configurePreferenceStorage, readPreference } from "../preferences";
import { useApp } from "../store";
import { setProjectMode } from "./projectMode";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

afterEach(() => configurePreferenceStorage(null));

describe("projectMode preference", () => {
  it("defaults to Info, for new and existing browsers alike", () => {
    configurePreferenceStorage(fakeStorage({ "vised-stage-skin": "warroom" }));
    expect(readPreference("projectMode")).toBe("info");
    configurePreferenceStorage(fakeStorage({ "vised-project-mode": "station" }));
    expect(readPreference("projectMode")).toBe("station");
  });
});

describe("setProjectMode", () => {
  it("turns the agent workstation on when Station opens, and leaves it on back in Info", () => {
    useApp.getState().updateUIPreference("agentWorkstation", false);
    useApp.getState().updateUIPreference("projectMode", "info");
    setProjectMode("station");
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
    setProjectMode("info");
    expect(useApp.getState().preferences.projectMode).toBe("info");
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });
});
