import { describe, expect, it } from "vitest";
import { localizeIssue, localizeIssueMessage } from "./issueMessages";

// The hub validates a graph in English only (internal/engine/validate.go) and
// sends no code with each finding, so the Problems panel maps the messages it
// knows to the catalog. Anything it does not know stays as the hub wrote it.

describe("validation messages in Traditional Chinese", () => {
  it.each([
    ["node is not connected to anything", "這個節點沒有連到任何節點。"],
    ['unknown node "build"', "引用了不存在的節點「build」。"],
    ['flag "beta" is not declared in graph flags', "旗標「beta」未在圖的 flags 中宣告。"],
    ["dependency cycle: a -> b -> a", "相依循環：a → b → a"],
    ["node depends on itself", "節點依賴自己。"],
    ['duplicate node id "x"', "節點 ID「x」重複。"],
    ["planned completion precedes planned start", "預計完成日早於預計開始日。"],
  ])("translates %j", (msg, expected) => {
    expect(localizeIssueMessage(msg, "zh-TW")).toBe(expected);
  });

  it("lists the undefined types with 、", () => {
    const msg =
      '3 node(s) use types this project does not define ("epic" ×2, "spike" ×1); they behave as Task. Pick a type for them, or add a custom type with that name.';
    const zh = localizeIssueMessage(msg, "zh-TW");
    expect(zh).toContain("3 個節點");
    expect(zh).toContain("「epic」×2、「spike」×1");
    expect(zh).not.toMatch(/[A-Za-z]{4,} [a-z]{3,}/);
  });

  it("keeps the hub's wording in English", () => {
    expect(localizeIssueMessage("node is not connected to anything", "en")).toBe(
      "node is not connected to anything",
    );
  });

  it("leaves a message it does not know as it is", () => {
    expect(localizeIssueMessage("something new from the hub", "zh-TW")).toBe("something new from the hub");
  });

  it("localizes an issue without touching its other fields", () => {
    const issue = { severity: "warning" as const, nodeId: "n1", field: "", msg: "node is not connected to anything" };
    expect(localizeIssue(issue, "zh-TW")).toEqual({ ...issue, msg: "這個節點沒有連到任何節點。" });
  });
});
