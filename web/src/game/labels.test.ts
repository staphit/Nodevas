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
