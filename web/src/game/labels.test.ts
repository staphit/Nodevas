import { describe, expect, it } from "vitest";
import { translate } from "../i18n";
import { actorLabel, type Translate } from "./labels";
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

describe("actionLabel for a run's workspace", () => {
  it("says Open workspace in both languages", async () => {
    const { actionLabel } = await import("./labels");
    const target = { kind: "run-workspace" as const, runId: "r", nodeId: "n" };
    expect(actionLabel(tEn, target, () => "")).toBe("Open workspace");
    expect(actionLabel(tZh, target, () => "")).toBe("開啟工作區");
  });
});

describe("global agents in labels", () => {
  it("names the way to Station's Global agents tab in both languages", async () => {
    const { actionLabel } = await import("./labels");
    const target = { kind: "global-agents" as const, runId: "r" };
    expect(actionLabel(tEn, target, () => "")).toBe("Show in Global agents");
    expect(actionLabel(tZh, target, () => "")).toBe("在全域 agent 中顯示");
  });

  it("says a headless global agent cannot ask", () => {
    const actor = {
      id: "run:r",
      kind: "run" as const,
      agent: "claude",
      model: "opus",
      project: "",
      area: "work" as const,
      pose: "working" as const,
      nodeTitle: "Bump the deps",
      runnerOnline: true,
      ownerName: "bea",
      heldMail: [],
      global: true,
      cannotAsk: true,
      target: { kind: "run-workspace" as const, runId: "r", nodeId: "" },
      actions: [],
    } satisfies Actor;
    expect(actorLabel(tEn, actor, "mac-mini")).toContain("it cannot ask you anything");
    expect(actorLabel(tZh, actor, "mac-mini")).toContain("無法向你提問");
    expect(actorLabel(tEn, { ...actor, cannotAsk: false }, "mac-mini")).not.toContain("cannot ask");
  });
});
