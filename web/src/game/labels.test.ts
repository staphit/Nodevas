import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { ACTIVITIES } from "./activity";
import {
  activityLabel,
  actorLabel,
  areaName,
  outdoorVisitorLabel,
  outingLabel,
  type Translate,
} from "./labels";
import { OUTING_KINDS, type Outing } from "./outing";
import type { Actor } from "./world";

const tZh: Translate = (key, values) => translate(key, "zh-TW", values);
const tEn: Translate = (key, values) => translate(key, "en", values);

describe("actorLabel with asks", () => {
  it("renders localized sentence for permission ask instead of backend English question", () => {
    const actorZh = {
      id: "a1",
      kind: "session" as const,
      model: "test",
      project: "alpha",
      area: "work" as const,
      agent: "antigravity",
      pose: "asking-user" as const,
      ask: {
        id: "ask-1",
        project: "alpha",
        sessionId: "s1",
        sessionName: "antigravity",
        actorId: "u1",
        kind: "permission" as const,
        tool: "run_command",
        question: "antigravity wants to run run_command: {cmd: 'ls'}",
        status: "pending" as const,
        createdAt: 1000,
      },
      heldMail: [],
    } as unknown as Actor;
    const labelZh = actorLabel(tZh, actorZh, "辦公室");
    expect(labelZh).toContain("antigravity 想要執行 run_command");
    expect(labelZh).not.toContain("antigravity wants to run");

    const labelEn = actorLabel(tEn, actorZh, "Office");
    expect(labelEn).toContain("antigravity wants to run run_command");
    expect(labelEn).not.toContain("antigravity wants to run run_command: {cmd: 'ls'}");
  });

  it("renders actionLabel for ask per kind", async () => {
    const { actionLabel } = await import("./labels");
    const noTitle = () => "";

    expect(actionLabel(tEn, { kind: "ask", askId: "1", askKind: "approval" }, noTitle)).toBe("Approve");
    expect(actionLabel(tZh, { kind: "ask", askId: "1", askKind: "approval" }, noTitle)).toBe("核准");

    expect(actionLabel(tEn, { kind: "ask", askId: "1", askKind: "permission" }, noTitle)).toBe("Review permission");
    expect(actionLabel(tZh, { kind: "ask", askId: "1", askKind: "permission" }, noTitle)).toBe("審核權限");

    expect(actionLabel(tEn, { kind: "ask", askId: "1", askKind: "choice" }, noTitle)).toBe("Choose");
    expect(actionLabel(tZh, { kind: "ask", askId: "1", askKind: "choice" }, noTitle)).toBe("選擇");

    expect(actionLabel(tEn, { kind: "ask", askId: "1", askKind: "question" }, noTitle)).toBe("Answer question");
    expect(actionLabel(tZh, { kind: "ask", askId: "1", askKind: "question" }, noTitle)).toBe("回答問題");
  });
});

describe("what an agent is doing, in words", () => {
  const base = {
    id: "run:r1",
    kind: "run" as const,
    agent: "claude",
    model: "opus",
    project: "main",
    area: "work" as const,
    runnerName: "mac-mini",
    runnerOnline: true,
    nodeTitle: "Login form",
    ownerName: "bea",
    heldMail: [],
    target: { kind: "node" as const, nodeId: "login" },
    actions: [],
  };
  const outing = (over: Partial<Outing> = {}): Outing => ({
    kind: "hike",
    step: 3,
    returnsAt: "2026-11-05T13:00:00Z",
    minutesLeft: 25,
    ...over,
  });

  it("names every activity and outing in both languages", () => {
    for (const activity of ACTIVITIES) {
      expect(activityLabel(tEn, activity)).not.toContain("game.");
      expect(activityLabel(tZh, activity)).toMatch(/\p{Script=Han}/u);
    }
    for (const kind of OUTING_KINDS) {
      expect(outingLabel(tEn, outing({ kind }))).not.toContain("game.");
      expect(outingLabel(tZh, outing({ kind }))).toMatch(/\p{Script=Han}/u);
    }
    expect(areaName(tEn, "outdoors")).toBe("Outdoors");
    expect(areaName(tZh, "outdoors")).toBe("戶外");
  });

  it("says the outing and when the agent is back", () => {
    expect(outingLabel(tEn, outing())).toBe("Out hiking, back in about 25 min.");
    expect(outingLabel(tZh, outing())).toBe("出去爬山了，大約 25 分鐘後回來。");
    expect(outingLabel(tEn, outing({ kind: "stream" }))).toBe("Out stream trekking, back in about 25 min.");
    expect(outingLabel(tZh, outing({ kind: "fish", minutesLeft: 1 }))).toBe("出去釣魚了，大約 1 分鐘後回來。");
    expect(outingLabel(tEn, outing({ kind: "camp", minutesLeft: 0 }))).toBe("Out camping, back any moment.");
    expect(outingLabel(tZh, outing({ kind: "camp", minutesLeft: 0 }))).toBe("出去露營了，隨時會回來。");
    expect(outingLabel(tEn, outing({ kind: "bbq" }))).toBe("Out having a camp barbecue, back in about 25 min.");
    expect(outingLabel(tZh, outing({ kind: "bbq" }))).toBe("出去露營烤肉了，大約 25 分鐘後回來。");
    expect(outingLabel(tEn, outing({ kind: "cycle", minutesLeft: 0 }))).toBe("Out cycling, back any moment.");
    expect(outingLabel(tZh, outing({ kind: "cycle", minutesLeft: 3 }))).toBe("出去騎單車了，大約 3 分鐘後回來。");
  });

  it("adds the activity to a working or reviewing agent's sentence", () => {
    const working = { ...base, pose: "working", activity: "edit" } as Actor;
    expect(actorLabel(tEn, working, "mac-mini")).toBe("Claude opus in mac-mini: working, on “Login form”. Now editing files.");
    expect(actorLabel(tZh, working, "mac-mini")).toBe("Claude opus（mac-mini）：工作中，處理「Login form」。 正在編輯檔案。");
    const reviewing = { ...base, pose: "reviewing", activity: "search" } as Actor;
    expect(actorLabel(tEn, reviewing, "mac-mini")).toContain("Now searching the code.");
    // A stale activity is never set, but a pose that is not at work ignores one.
    const failed = { ...base, pose: "failed", activity: "edit" } as Actor;
    expect(actorLabel(tEn, failed, "mac-mini")).not.toContain("Now");
  });

  it("adds the outing to a limit agent's sentence", () => {
    const out = { ...base, area: "outdoors", pose: "limit", outing: outing() } as Actor;
    expect(actorLabel(tEn, out, "mac-mini")).toBe(
      "Claude opus in mac-mini: waiting for its usage limit to reset, on “Login form”. Out hiking, back in about 25 min.",
    );
    expect(actorLabel(tZh, out, "mac-mini")).toContain("出去爬山了，大約 25 分鐘後回來。");
  });

  it("names an outdoor visitor like a lounge visitor", () => {
    const visitor = {
      project: "app",
      label: "APP",
      agent: "codex",
      model: "o3",
      title: "Search",
      outing: outing({ kind: "fish", minutesLeft: 0 }),
      target: { kind: "project" as const, project: "app" },
      actions: [],
    };
    expect(outdoorVisitorLabel(tEn, visitor)).toBe("Codex o3 from APP: Out fishing, back any moment.");
    expect(outdoorVisitorLabel(tZh, visitor)).toBe("來自 APP 的 Codex o3：出去釣魚了，隨時會回來。");
  });
});
