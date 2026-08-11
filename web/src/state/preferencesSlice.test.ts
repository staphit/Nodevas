import { beforeEach, describe, expect, it } from "vitest";
import { configurePreferenceStorage, readPreferences } from "../preferences";
import { useApp } from "../store";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

beforeEach(() => {
  configurePreferenceStorage(fakeStorage());
  // Slice actions read/write through the preferences module directly rather
  // than the store's own initial values, so state is synced by hand here the
  // way the slice's own `updateUIPreference` does after every write.
  useApp.setState({
    preferences: readPreferences(),
    theme: readPreferences().theme,
    paneOpen: { graph: readPreferences().paneGraph, timeline: readPreferences().paneTimeline },
  });
});

describe("toggleTheme", () => {
  it("flips light/dark and persists it as a preference", () => {
    useApp.setState({ theme: "dark" });

    useApp.getState().toggleTheme();

    expect(useApp.getState().theme).toBe("light");
    expect(useApp.getState().preferences.theme).toBe("light");

    useApp.getState().toggleTheme();
    expect(useApp.getState().theme).toBe("dark");
  });
});

describe("togglePane", () => {
  it("toggles the named pane", () => {
    useApp.setState({ paneOpen: { graph: true, timeline: true } });

    useApp.getState().togglePane("graph");

    expect(useApp.getState().paneOpen).toEqual({ graph: false, timeline: true });
  });

  // Both panes hidden would leave an empty stage, so closing the second one
  // open re-opens the other rather than leaving nothing visible.
  it("refuses to close the last open pane, falling back to the other one", () => {
    useApp.setState({ paneOpen: { graph: false, timeline: true } });

    useApp.getState().togglePane("timeline");

    expect(useApp.getState().paneOpen).toEqual({ graph: true, timeline: false });
  });

  it("refuses to close the last open pane in the other direction too", () => {
    useApp.setState({ paneOpen: { graph: true, timeline: false } });

    useApp.getState().togglePane("graph");

    expect(useApp.getState().paneOpen).toEqual({ graph: false, timeline: true });
  });
});

describe("updateUIPreference", () => {
  it("writes through to storage and mirrors the normalized value into state", () => {
    useApp.getState().updateUIPreference("explorerWidth", 5000);

    // Clamped by the preference's own normalize step.
    expect(useApp.getState().preferences.explorerWidth).toBe(520);
  });

  it("derives theme and paneOpen from the preference it just wrote", () => {
    useApp.getState().updateUIPreference("theme", "dark");
    expect(useApp.getState().theme).toBe("dark");

    useApp.getState().updateUIPreference("paneGraph", false);
    useApp.getState().updateUIPreference("paneTimeline", false);
    // syncDerivedPreferences refuses to let both panes end up closed.
    expect(useApp.getState().paneOpen.timeline).toBe(true);
  });
});

describe("resetUIPreferences", () => {
  it("resets one scope and leaves preferences outside it untouched", () => {
    useApp.getState().updateUIPreference("explorerWidth", 400);
    useApp.getState().updateUIPreference("theme", "dark");

    useApp.getState().resetUIPreferences("explorer");

    expect(useApp.getState().preferences.explorerWidth).toBe(304);
    expect(useApp.getState().preferences.theme).toBe("dark");
  });

  it("resets everything when no scope is given", () => {
    useApp.getState().updateUIPreference("explorerWidth", 400);
    useApp.getState().updateUIPreference("editorFontSize", 20);

    useApp.getState().resetUIPreferences();

    expect(useApp.getState().preferences.explorerWidth).toBe(304);
    expect(useApp.getState().preferences.editorFontSize).toBe(14);
  });
});
