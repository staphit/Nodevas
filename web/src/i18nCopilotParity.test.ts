import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { hasMessage, translate } from "./i18n";

// There is no global en/zh-TW parity check, and translate() falls back to
// English, so a key missing from one catalog ships half-translated without
// a sound. The copilot's and its drafts' keys are held to both catalogs.
const FAMILIES = [
  "copilot.",
  "draft.",
  "undo.label.",
  "runDialog.",
  "picker.",
  "globalAgents.group.",
  "busy.",
  "overlap.",
  "contest.",
];

/** Keys of one catalog, read from the source so none is missed. */
function catalogKeys(language: "en" | "zh-TW"): string[] {
  const text = readFileSync(resolve(process.cwd(), "src/i18n.ts"), "utf8");
  const zhStart = text.indexOf('\n  "zh-TW": {');
  const end = text.indexOf("\nfunction formatMessage");
  const enStart = text.indexOf("\n  en: {");
  const section = language === "en" ? text.slice(enStart, zhStart) : text.slice(zhStart, end);
  const keys: string[] = [];
  for (const line of section.split("\n")) {
    const match = /^\s{4}"([^"]+)":/.exec(line);
    if (match) keys.push(match[1]);
  }
  return keys;
}

const inFamily = (key: string) => FAMILIES.some((family) => key.startsWith(family));

describe("copilot and draft keys in both catalogs", () => {
  it("reads both catalogs", () => {
    expect(catalogKeys("en").length).toBeGreaterThan(2000);
    expect(catalogKeys("zh-TW").length).toBeGreaterThan(2000);
  });

  it("has every en key in zh-TW and the other way round", () => {
    const en = catalogKeys("en").filter(inFamily);
    const zh = catalogKeys("zh-TW").filter(inFamily);
    expect(en.filter((key) => !hasMessage("zh-TW", key))).toEqual([]);
    expect(zh.filter((key) => !hasMessage("en", key))).toEqual([]);
    expect(en.length).toBeGreaterThan(100);
  });

  it("reads the draft keys in Chinese, with 節點 for node", () => {
    expect(
      translate("draft.title", "zh-TW", {
        nodeText: translate("draft.glyphCount", "zh-TW", { n: 8 }),
        linkText: translate("draft.linkCount", "zh-TW", { n: 7 }),
      }),
    ).toBe("草稿：8 個節點、7 條依賴");
    expect(translate("draft.create", "zh-TW", { n: 8 })).toBe("建立 8 個節點");
    expect(translate("draft.created", "zh-TW", { n: 8 })).toBe("已建立 8 個節點");
    const zh = catalogKeys("zh-TW").filter((key) => key.startsWith("draft.") || key.startsWith("copilot."));
    const english = zh.filter((key) => /\bnodes?\b/i.test(translate(key, "zh-TW")));
    expect(english).toEqual([]);
  });

  it("has the plan draft keys in both catalogs", () => {
    const keys = [
      "draft.plan.origin",
      "draft.plan.agentUnknown",
      "draft.plan.note",
      "draft.plan.tags",
      "draft.plan.acceptance",
      "draft.plan.acceptanceUnused",
      "draft.issue.body_clipped",
      "copilot.planOpen",
      "copilot.placeholder.planOpen",
    ];
    for (const key of keys) {
      expect(hasMessage("en", key), key).toBe(true);
      expect(hasMessage("zh-TW", key), key).toBe(true);
    }
    expect(translate("draft.plan.tags", "zh-TW", { n: 3 })).toBe("3 個標籤");
    expect(translate("draft.plan.acceptance", "zh-TW", { n: 2 })).toBe("2 條驗收條件");
    expect(translate("draft.plan.origin", "en", { agent: "claude", goal: "Login page" })).toBe(
      "Planned by claude for “Login page”",
    );
    expect(translate("draft.plan.origin", "zh-TW", { agent: "claude", goal: "Login" })).toBe("由 claude 為「Login」規劃");
  });

  it("says the plan's tasks are not on the board yet, not that the board is empty", () => {
    // A plan draft follows start_breakdown, which has already put the goal on
    // the board; only the tasks are missing.
    expect(translate("draft.plan.note", "en")).toBe(
      "These tasks are not on the board yet. Create adds them and makes the goal wait for them.",
    );
    expect(translate("draft.plan.note", "zh-TW")).toBe("這些任務還沒放上看板。按「建立」會加入它們，並讓目標等它們完成。");
  });

  it("names one selected node in the singular", () => {
    expect(translate("copilot.selectionChipOne", "en")).toBe("After 1 selected node");
    expect(translate("copilot.selectionChip", "en", { n: 3 })).toBe("After 3 selected nodes");
    expect(translate("copilot.selectionChipOne", "zh-TW")).toBe("接在 1 個選取節點之後");
  });

  it("says 雙擊 for double-click everywhere in zh-TW", () => {
    const keys = catalogKeys("zh-TW").filter((key) => translate(key, "zh-TW").includes("按兩下"));
    expect(keys).toEqual([]);
    expect(translate("draft.rename", "zh-TW")).toBe("雙擊重新命名");
  });

  it("says 點一下 or 點擊 for click, never 按一下", () => {
    const keys = catalogKeys("zh-TW").filter((key) => translate(key, "zh-TW").includes("按一下"));
    expect(keys).toEqual([]);
  });

  it("has a singular for every count a one-node draft or a one-tag plan shows", () => {
    for (const key of [
      "draft.presenter.lineOne",
      "draft.presenter.nodeOne",
      "draft.plan.tagsOne",
      "draft.plan.acceptanceOne",
      "draft.undoKeptNone",
    ]) {
      expect(catalogKeys("en")).toContain(key);
      expect(catalogKeys("zh-TW")).toContain(key);
    }
    expect(translate("draft.presenter.lineOne", "en")).not.toMatch(/\b1 steps\b/);
    expect(translate("draft.plan.tagsOne", "en")).toBe("1 tag");
  });

  it("covers the terminal dialog, picker, group, busy, overlap and contest keys and keeps globalAgents.noAsk intact", () => {
    for (const family of ["runDialog.", "picker.", "globalAgents.group.", "busy.", "overlap.", "contest."]) {
      expect(catalogKeys("en").some((key) => key.startsWith(family)), family).toBe(true);
    }
    expect(hasMessage("en", "globalAgents.replyNote")).toBe(true);
    expect(hasMessage("zh-TW", "globalAgents.replyNote")).toBe(true);
    expect(translate("terminal.modeNoteLocal", "en")).toBe(
      "Runs the agent's own terminal on this computer and shows it here; you can type to it.",
    );
    expect(translate("terminal.modeNote", "en")).toBe(
      "Runs the agent's own terminal on the runner and shows it here; you can type to it.",
    );
    expect(translate("globalAgents.noAsk", "en")).toBe(
      "Runs in the background: it cannot ask you anything. Start one Interactive to talk with it.",
    );
  });
});
