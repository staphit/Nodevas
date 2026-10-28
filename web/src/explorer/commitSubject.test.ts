import { describe, expect, it } from "vitest";
import { commitSubjectText } from "./commitSubject";

// The hub writes a commit's subject from its paths and the kind of each
// change, in English: "Auth API: edit page; Billing: create; graph: edit".
// The Git tab shows it in the reader's language; anything it does not
// recognise (an agent's own message) stays as written.
describe("a commit subject in the reader's language", () => {
  it.each([
    ["graph: create", "關係圖：建立"],
    ["甲節點: create", "甲節點：建立"],
    ["乙節點: create; graph: edit", "乙節點：建立；關係圖：編輯"],
    ["graph: outside edit", "關係圖：外部編輯"],
    ["甲節點: edit, edit page, add page, delete page, pages", "甲節點：編輯、編輯子頁、新增子頁、刪除子頁、子頁清單"],
    ["甲節點: move; 乙節點: delete; 丙: restore, convert page", "甲節點：移動；乙節點：刪除；丙：還原、轉換子頁"],
    ["a: create; b: edit; and 1 more", "a：建立；b：編輯；還有 1 項"],
    ["update", "更新"],
  ])("%s reads %s in zh-TW", (subject, expected) => {
    expect(commitSubjectText(subject, "zh-TW")).toBe(expected);
  });

  it("translates what is left of a subject cut short at its limit", () => {
    expect(commitSubjectText("甲節點: edit, edit page, pa…", "zh-TW")).toBe("甲節點：編輯、編輯子頁、pa…");
    expect(commitSubjectText("甲節點: create; 乙節點: edit…", "zh-TW")).toBe("甲節點：建立；乙節點：編輯…");
  });

  it("keeps a subject it does not recognise as written", () => {
    expect(commitSubjectText("login: done", "zh-TW")).toBe("login: done");
    expect(commitSubjectText("Partial work: the agent stopped (timeout) before finishing.", "zh-TW")).toBe(
      "Partial work: the agent stopped (timeout) before finishing.",
    );
    expect(commitSubjectText("", "zh-TW")).toBe("");
  });

  it("reads exactly as written in English", () => {
    for (const subject of [
      "graph: create",
      "乙節點: create; graph: edit",
      "甲節點: edit, edit page, pa…",
      "a: create; b: edit; and 1 more",
      "update",
      "login: done",
    ]) {
      expect(commitSubjectText(subject, "en")).toBe(subject);
    }
  });
});
