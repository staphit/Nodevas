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

// Easy start's choices follow the person (account), like the tour's answer:
// one person's first-run answers and preset hold on every browser they use.
describe("easy start preferences", () => {
  it.each([
    ["firstRun", "vised-first-run", "onboarding"],
    ["experience", "vised-experience", "onboarding"],
    ["simpleAgentKind", "vised-simple-agent-kind", "agents"],
    ["simpleAgentRepoId", "vised-simple-agent-repo", "agents"],
  ] as const)("%s is account-synced under %s", (name, key, scope) => {
    const spec = PREFERENCE_SCHEMA[name];
    expect(spec.key).toBe(key);
    expect(spec.sync).toBe("account");
    expect(spec.scope).toBe(scope);
    expect(ACCOUNT_PREFERENCE_KEYS).toContain(name);
  });

  it("start at their defaults", () => {
    expect(readPreference("firstRun")).toBe("pending");
    expect(readPreference("experience")).toBe("full");
    expect(readPreference("simpleAgentKind")).toBe("auto");
    expect(readPreference("simpleAgentRepoId")).toBe("");
  });

  it.each([
    ["vised-first-run", "firstRun", "asked", "pending"],
    ["vised-experience", "experience", "expert", "full"],
    ["vised-simple-agent-kind", "simpleAgentKind", "copilot", "auto"],
  ] as const)("an unknown stored %s falls back", (key, name, stored, fallback) => {
    configurePreferenceStorage(fakeStorage({ [key]: stored }));
    expect(readPreference(name)).toBe(fallback);
  });

  it.each(["pending", "done", "skipped", "existing"] as const)("keeps firstRun %s", (value) => {
    configurePreferenceStorage(fakeStorage({ "vised-first-run": value }));
    expect(readPreference("firstRun")).toBe(value);
  });

  it.each(["auto", "claude", "codex", "gemini", "opencode", "antigravity"] as const)(
    "keeps the agent kind %s",
    (value) => {
      configurePreferenceStorage(fakeStorage({ "vised-simple-agent-kind": value }));
      expect(readPreference("simpleAgentKind")).toBe(value);
    },
  );

  it("writes and reads back the preset", () => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    writePreference("experience", "simple");
    expect(storage.map.get("vised-experience")).toBe("simple");
    expect(readPreference("experience")).toBe("simple");
  });
});

// The repo id is the runner's (validRepoID: at most 256 bytes, no control
// characters); anything else stored is not one and reads as "first repo".
describe("the describe box's folder id", () => {
  it.each(["", "repo1", "a".repeat(256), "C:/code/app", "我的專案"])("keeps %j", (stored) => {
    configurePreferenceStorage(fakeStorage({ "vised-simple-agent-repo": stored }));
    expect(readPreference("simpleAgentRepoId")).toBe(stored);
  });

  it.each(["a".repeat(257), "repo\u0000", "re\npo", "tab\there", "del\u007f"])("refuses %j", (stored) => {
    configurePreferenceStorage(fakeStorage({ "vised-simple-agent-repo": stored }));
    expect(readPreference("simpleAgentRepoId")).toBe("");
  });

  // The runner counts UTF-8 bytes, as Go's len does.
  it("counts bytes, not characters", () => {
    configurePreferenceStorage(fakeStorage({ "vised-simple-agent-repo": "專".repeat(85) }));
    expect(readPreference("simpleAgentRepoId")).toBe("專".repeat(85));
    configurePreferenceStorage(fakeStorage({ "vised-simple-agent-repo": "專".repeat(86) }));
    expect(readPreference("simpleAgentRepoId")).toBe("");
  });

  it("writes an id as it is", () => {
    const storage = fakeStorage();
    configurePreferenceStorage(storage);
    writePreference("simpleAgentRepoId", "repo-7");
    expect(storage.map.get("vised-simple-agent-repo")).toBe("repo-7");
  });
});
