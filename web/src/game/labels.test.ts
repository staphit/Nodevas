import { describe, expect, it } from "vitest";
import { translate, type MessageValues } from "../i18n";
import { actorLabel, type Translate } from "./labels";
import type { Actor } from "./world";

const tZh: Translate = (key: string, values?: MessageValues) => translate(key, "zh-TW", values);
const tEn: Translate = (key: string, values?: MessageValues) => translate(key, "en", values);

describe("actorLabel with asks", () => {
  it("renders localized sentence for permission ask instead of backend English question", () => {
    const actorZh: Actor = {
      id: "a1",
      agent: "antigravity",
      pose: "asking-user",
      ask: {
        id: "ask-1",
        project: "alpha",
        sessionId: "s1",
        sessionName: "antigravity",
        actorId: "u1",
        kind: "permission",
        tool: "run_command",
        question: "antigravity wants to run run_command: {cmd: 'ls'}",
        status: "pending",
        createdAt: 1000,
      },
      heldMail: [],
    };
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
