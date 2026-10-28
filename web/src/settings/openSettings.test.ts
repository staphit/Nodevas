import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { ADVANCED_ROWS, SETTINGS_PLACES, sectionFor, type SettingsSection } from "./openSettings";
import { searchSettings, SETTINGS_ENTRIES, type SettingsNeed } from "./settingsSearch";

const ALL: SettingsSection[] = [
  "project",
  "appearance",
  "notifications",
  "backup",
  "workstation",
  "runners",
  "usage",
  "budget",
  "routing",
  "copilot",
  "server",
  "account",
  "audit",
];

describe("where the old parts live", () => {
  it("maps every old part to a section", () => {
    expect(ALL.map((id) => [id, SETTINGS_PLACES[id].page])).toEqual([
      ["project", "project"],
      ["appearance", "general"],
      ["notifications", "general"],
      ["backup", "project"],
      ["workstation", "agents"],
      ["runners", "agents"],
      ["usage", "agents"],
      ["budget", "agents"],
      ["routing", "agents"],
      ["copilot", "general"],
      ["server", "account"],
      ["account", "account"],
      ["audit", "account"],
    ]);
  });

  it("opens budget, routing and usage as rows of Agents › Advanced", () => {
    for (const id of ["budget", "routing", "usage"] as const) {
      const row = SETTINGS_PLACES[id].row;
      expect(row).toBe(id);
      expect(ADVANCED_ROWS).toContain(row);
    }
  });

  it("remembers a place as the part that names it", () => {
    for (const id of ALL) {
      const place = SETTINGS_PLACES[id];
      // Runners and the workstation are one section now.
      expect(sectionFor(place.page, place.row ?? null)).toBe(id === "runners" ? "workstation" : id);
    }
    // A row without a part of its own is remembered as its section.
    expect(sectionFor("agents", "autoAccept")).toBe("workstation");
    expect(sectionFor("project", "statuses")).toBe("project");
    expect(sectionFor("general", "layout")).toBe("appearance");
  });
});

describe("settings search", () => {
  const en = (key: string, values?: Record<string, string | number>) => translate(key, "en", values);
  const zh = (key: string, values?: Record<string, string | number>) => translate(key, "zh-TW", values);
  const everything = () => true;

  it("finds by label or section, in the reader's language, any case", () => {
    const budget = searchSettings("BUDGET", en, everything);
    expect(budget.map((hit) => [hit.label, hit.where])).toEqual([["Daily budget", "Agents › Advanced"]]);
    expect(budget[0].entry).toMatchObject({ page: "agents", row: "budget" });

    expect(searchSettings("預算", zh, everything).map((hit) => hit.where)).toEqual(["Agent 與 Runner › 進階"]);
    expect(searchSettings("this project", en, everything).length).toBeGreaterThan(3);
    expect(searchSettings("   ", en, everything)).toEqual([]);
  });

  it("offers only what this install has", () => {
    const standalone = (need: SettingsNeed) => need === "workstation";
    expect(searchSettings("audit", en, standalone)).toEqual([]);
    expect(searchSettings("audit", en, everything).map((hit) => hit.entry.row)).toEqual(["audit"]);
    const hidden = () => false;
    expect(searchSettings("runner", en, hidden)).toEqual([]);
  });

  it("has a label in both languages for every entry", () => {
    for (const entry of SETTINGS_ENTRIES) {
      expect(en(entry.label)).not.toBe(entry.label);
      expect(zh(entry.label)).not.toBe(entry.label);
    }
  });
});
