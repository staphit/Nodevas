import { renderHook } from "@testing-library/react";
import { EditorState } from "@codemirror/state";
import { afterEach, describe, expect, it } from "vitest";
import { readPreferences } from "../../preferences";
import { useApp } from "../../store";
import { useEditorExtensions } from "./editorExtensions";

// The words CodeMirror itself draws: the search panel (Ctrl+F), the fold
// gutter, go-to-line, completion and the screen-reader announcements.
const CODEMIRROR_PHRASES = [
  "Find",
  "Replace",
  "next",
  "previous",
  "all",
  "match case",
  "regexp",
  "by word",
  "replace",
  "replace all",
  "close",
  "current match",
  "on line",
  "replaced match on line $",
  "replaced $ matches",
  "Go to line",
  "go",
  "Fold line",
  "Unfold line",
  "Folded lines",
  "Unfolded lines",
  "folded code",
  "unfold",
  "to",
  "Completions",
  "Control character",
  "Selection deleted",
];

function editorState(format: "md" | "txt" | "html") {
  const { result } = renderHook(() =>
    useEditorExtensions({
      format,
      editorMode: "source",
      links: [],
      currentProject: "",
      onOpenNodeLink: () => {},
    }),
  );
  return EditorState.create({ extensions: result.current });
}

afterEach(() => {
  useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
});

describe("the editor's own words follow the interface language", () => {
  it.each(["md", "txt", "html"] as const)("are Traditional Chinese for a %s page in zh-TW", (format) => {
    useApp.setState({ preferences: { ...readPreferences(), language: "zh-TW" } });
    const state = editorState(format);
    for (const phrase of CODEMIRROR_PHRASES) {
      const translated = state.phrase(phrase);
      expect(translated, phrase).not.toBe(phrase);
      expect(translated, phrase).toMatch(/[一-鿿]/);
    }
    expect(state.phrase("replaced $ matches", 3)).toContain("3");
    expect(state.phrase("replaced match on line $", 12)).toContain("12");
  });

  it("stay English in English", () => {
    useApp.setState({ preferences: { ...readPreferences(), language: "en" } });
    const state = editorState("md");
    expect(state.phrase("Find")).toBe("Find");
    expect(state.phrase("Unfold line")).toBe("Unfold line");
  });
});
