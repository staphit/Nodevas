import { beforeEach, describe, expect, it } from "vitest";
import { PREFERENCE_SCHEMA } from "./schema";
import {
  configurePreferenceStorage,
  readPreference,
  resetPreferences,
  subscribePreferences,
  writePreference,
} from "./storage";

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
  configurePreferenceStorage(null);
});

describe("validation", () => {
  it("clamps an out-of-range stored width instead of resetting it", () => {
    configurePreferenceStorage(fakeStorage({ "vised-explorer-width": "5000" }));
    expect(readPreference("explorerWidth")).toBe(520);
  });

  it("falls back when the stored value is unusable", () => {
    configurePreferenceStorage(
      fakeStorage({
        "vised-explorer-width": "not-a-number",
        "vised-timeline-orientation": "sideways",
        "vised-explorer-expanded-projects": "{oops",
      }),
    );
    expect(readPreference("explorerWidth")).toBe(304);
    expect(readPreference("timelineOrientation")).toBe("vertical");
    expect(readPreference("explorerExpandedProjects")).toEqual([]);
  });

  // The explorer default is a thunk over the window width: a phone-sized
  // window starts with the overlay out of the way, a desktop one starts with
  // the tree showing. It is only a default — a stored answer always wins.
  it("collapses the explorer by default on a phone-width window", () => {
    configurePreferenceStorage(fakeStorage());
    vi.stubGlobal("innerWidth", 390);
    try {
      expect(readPreference("explorerCollapsed")).toBe(true);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("keeps a stored explorer choice over the width fallback", () => {
    configurePreferenceStorage(
      fakeStorage({ "vised-explorer-collapsed": "0" }),
    );
    vi.stubGlobal("innerWidth", 390);
    try {
      expect(readPreference("explorerCollapsed")).toBe(false);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("clamps on write and returns what was stored", () => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    expect(writePreference("editorFontSize", 99)).toBe(22);
    expect(storage.map.get("vised-editor-fs")).toBe("22");
  });

  it("uses compact encodings for boolean and numeric preferences", () => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    writePreference("paneGraph", false);
    writePreference("paneSplit", 42.125);
    expect(storage.map.get(PREFERENCE_SCHEMA.paneGraph.key)).toBe("0");
    expect(storage.map.get(PREFERENCE_SCHEMA.paneSplit.key)).toBe("42.13");
  });
});

describe("reset", () => {
  it("resets one scope and leaves the others alone", () => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    writePreference("timelineCellWidth", 300);
    writePreference("explorerWidth", 400);
    writePreference("theme", "dark");

    const values = resetPreferences("timeline");
    expect(values.timelineCellWidth).toBe(220);
    expect(values.explorerWidth).toBe(400);
    expect(values.theme).toBe("dark");
  });

  it("resets everything when no scope is given", () => {
    configurePreferenceStorage(fakeStorage());
    writePreference("explorerWidth", 400);
    writePreference("editorFontSize", 20);
    const values = resetPreferences();
    expect(values.explorerWidth).toBe(304);
    expect(values.editorFontSize).toBe(14);
  });
});

describe("subscriptions", () => {
  it("notifies on write and stops after unsubscribe", () => {
    configurePreferenceStorage(fakeStorage());
    const seen: string[] = [];
    const unsubscribe = subscribePreferences((key) => seen.push(key));
    writePreference("theme", "dark");
    unsubscribe();
    writePreference("editorFontSize", 16);
    expect(seen).toEqual(["theme"]);
  });
});
