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

  it("closes the node panel the board had open on the way into Station, keeping its tab", () => {
    // The panel is an overlay in Station: carried over from the board it
    // covered the emergency stop and the side column (UX sweep round 3).
    useApp.getState().updateUIPreference("projectMode", "info");
    useApp.setState({ activeTab: "n1" });
    setProjectMode("station");
    expect(useApp.getState().activeTab).toBeNull();
  });

  it("keeps a node panel opened inside Station while Station stays shown", () => {
    useApp.getState().updateUIPreference("projectMode", "station");
    useApp.setState({ activeTab: "n1" });
    setProjectMode("station");
    expect(useApp.getState().activeTab).toBe("n1");
    useApp.setState({ activeTab: null });
  });
});
