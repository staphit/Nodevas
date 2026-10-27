import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { configurePreferenceStorage, readPreference } from "../preferences";
import { useApp } from "../store";
import { setProjectMode, stationView, useStageSkinItems } from "./stageSkin";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

afterEach(() => configurePreferenceStorage(null));

describe("stageSkin preference", () => {
  it("defaults to the cards, for new and existing browsers alike", () => {
    configurePreferenceStorage(fakeStorage({ "vised-pane-graph": "1", "vised-theme": "dark" }));
    expect(readPreference("stageSkin")).toBe("cards");
  });

  it("keeps a stored skin and ignores one it does not know", () => {
    configurePreferenceStorage(fakeStorage({ "vised-stage-skin": "dungeon" }));
    expect(readPreference("stageSkin")).toBe("dungeon");
    configurePreferenceStorage(fakeStorage({ "vised-stage-skin": "kanban" }));
    expect(readPreference("stageSkin")).toBe("cards");
  });
});

describe("projectMode preference", () => {
  it("defaults to Info, for new and existing browsers alike", () => {
    configurePreferenceStorage(fakeStorage({ "vised-stage-skin": "warroom" }));
    expect(readPreference("projectMode")).toBe("info");
    configurePreferenceStorage(fakeStorage({ "vised-project-mode": "station" }));
    expect(readPreference("projectMode")).toBe("station");
  });
});

describe("Station's views", () => {
  function reset() {
    useApp.getState().updateUIPreference("language", "en");
    useApp.getState().updateUIPreference("agentWorkstation", false);
    useApp.getState().updateUIPreference("projectMode", "info");
    useApp.getState().updateUIPreference("stageSkin", "cards");
  }

  it("reads the old cards default as the war room", () => {
    expect(stationView("cards")).toBe("warroom");
    expect(stationView("warroom")).toBe("warroom");
    expect(stationView("dungeon")).toBe("dungeon");
  });

  it("offers both views from Info's graph menu, each opening Station on it", () => {
    reset();
    const { result } = renderHook(() => useStageSkinItems());
    expect(result.current.map((item) => item.label)).toEqual(["Station: War room", "Station: Boss map"]);
    result.current[1].onSelect();
    const prefs = useApp.getState().preferences;
    expect(prefs.projectMode).toBe("station");
    expect(prefs.stageSkin).toBe("dungeon");
  });

  it("turns the agent workstation on when Station opens, and leaves it on back in Info", () => {
    reset();
    setProjectMode("station");
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
    setProjectMode("info");
    expect(useApp.getState().preferences.projectMode).toBe("info");
    expect(useApp.getState().preferences.agentWorkstation).toBe(true);
  });
});
