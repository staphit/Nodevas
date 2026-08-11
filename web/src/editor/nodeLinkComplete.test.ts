import { describe, expect, it } from "vitest";
import { EditorState } from "@codemirror/state";
import { CompletionContext } from "@codemirror/autocomplete";
import { nodeLinkCompletionSource } from "./nodeLinkComplete";

const links = [
  { label: "角色設定", node: "node-0006" },
  { label: "主線", project: "Story", node: "node-0012" },
];

function complete(text: string, explicit = false) {
  const state = EditorState.create({ doc: text, selection: { anchor: text.length } });
  const context = new CompletionContext(state, text.length, explicit);
  return nodeLinkCompletionSource(() => ({ links, currentProject: "Current" }))(
    context,
  );
}

describe("node link completion", () => {
  it("offers the node's links after a slash", () => {
    const result = complete("見 /");
    expect(result?.options.map((option) => option.label)).toEqual([
      "/角色設定",
      "/主線",
    ]);
  });

  it("writes a full link, keeping the project only when it differs", () => {
    const options = complete("見 /")!.options;
    expect(options[0].apply).toBe("[[node-0006|角色設定]]");
    expect(options[1].apply).toBe("[[Story/node-0012|主線]]");
  });

  it("stays out of paths and dates", () => {
    expect(complete("src/")).toBeNull();
    expect(complete("2026/08")).toBeNull();
  });
});
