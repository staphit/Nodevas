import { act, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it } from "vitest";
import { configurePreferenceStorage, readPreference, readPreferences } from "../preferences";
import { EditionProvider, type Edition } from "../runners/edition";
import { useApp } from "../store";
import { EXPERIENCES, setExperience, SIMPLE_OVERRIDE_REASON, useSimpleMode } from "./simpleMode";

function fakeStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    map,
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

function start(stored: Record<string, string> = {}) {
  const storage = fakeStorage(stored);
  configurePreferenceStorage(storage);
  useApp.setState({ preferences: readPreferences() });
  return storage;
}

const inEdition =
  (edition: Edition) =>
  ({ children }: { children?: ReactNode }) => <EditionProvider edition={edition}>{children}</EditionProvider>;

beforeEach(() => {
  start();
});

describe("useSimpleMode", () => {
  it.each([
    ["simple", "single", true],
    ["full", "single", false],
    ["simple", "team", false],
    ["full", "team", false],
  ] as const)("pref %s in edition %s is %s", (pref, edition, expected) => {
    start({ "vised-experience": pref });
    const { result } = renderHook(() => useSimpleMode(), { wrapper: inEdition(edition) });
    expect(result.current).toBe(expected);
  });

  // Outside AuthGate (no provider) the app reads as a team hub: the account
  // pref alone never turns the preset on there.
  it("is off with no edition provider, whatever the pref says", () => {
    start({ "vised-experience": "simple" });
    const { result } = renderHook(() => useSimpleMode());
    expect(result.current).toBe(false);
  });

  it("follows a change of the pref", () => {
    const { result } = renderHook(() => useSimpleMode(), { wrapper: inEdition("single") });
    expect(result.current).toBe(false);
    act(() => setExperience("simple"));
    expect(result.current).toBe(true);
    act(() => setExperience("full"));
    expect(result.current).toBe(false);
  });
});

describe("setExperience", () => {
  it("writes the account pref under its storage key", () => {
    const storage = start();
    setExperience("simple");
    expect(readPreference("experience")).toBe("simple");
    expect(useApp.getState().preferences.experience).toBe("simple");
    expect(storage.map.get("vised-experience")).toBe("simple");
  });
});

describe("the contract's constants", () => {
  it("names the two experiences and the fixed override reason", () => {
    expect(EXPERIENCES).toEqual(["full", "simple"]);
    expect(SIMPLE_OVERRIDE_REASON).toBe("Simple mode: started without checks");
    // The hub caps an override reason at 200 characters.
    expect(SIMPLE_OVERRIDE_REASON.length).toBeLessThanOrEqual(200);
  });
});
