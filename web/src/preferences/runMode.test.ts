import { beforeEach, describe, expect, it } from "vitest";
import { ACCOUNT_PREFERENCE_KEYS, PREFERENCE_SCHEMA } from "./schema";
import { configurePreferenceStorage, readPreference, writePreference } from "./storage";

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
});

describe("runMode preference", () => {
  it("is account-synced under vised-run-mode in the agents scope with default auto", () => {
    const spec = PREFERENCE_SCHEMA.runMode;
    expect(spec.key).toBe("vised-run-mode");
    expect(spec.sync).toBe("account");
    expect(spec.scope).toBe("agents");
    expect(ACCOUNT_PREFERENCE_KEYS).toContain("runMode");
    expect(readPreference("runMode")).toBe("auto");
  });

  it.each(["auto", "interactive", "background"] as const)("stores and reads back %s", (value) => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    writePreference("runMode", value);
    expect(storage.map.get("vised-run-mode")).toBe(value);
    expect(readPreference("runMode")).toBe(value);
  });

  it("falls back to auto on an unknown stored value", () => {
    configurePreferenceStorage(fakeStorage({ "vised-run-mode": "headless" }));
    expect(readPreference("runMode")).toBe("auto");
  });
});
