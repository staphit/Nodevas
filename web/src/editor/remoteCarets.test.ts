import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { describe, expect, it } from "vitest";
import { remoteCarets, setRemoteCarets, type RemoteCaret } from "./remoteCarets";

function editor(doc: string) {
  const view = new EditorView({
    state: EditorState.create({ doc, extensions: [remoteCarets()] }),
  });
  return view;
}

function caret(overrides: Partial<RemoteCaret> = {}): RemoteCaret {
  return {
    peerId: "peer-1",
    name: "Ann",
    color: "hsl(200 70% 45%)",
    anchor: 0,
    head: 0,
    ...overrides,
  };
}

/** How many decorations the field currently holds. */
function decorationCount(view: EditorView): number {
  let count = 0;
  const set = view.state.facet(EditorView.decorations);
  for (const source of set) {
    const decorations = typeof source === "function" ? source(view) : source;
    const cursor = decorations.iter();
    while (cursor.value) {
      count++;
      cursor.next();
    }
  }
  return count;
}

describe("remoteCarets", () => {
  it("draws a caret and its selection", () => {
    const view = editor("hello world");
    view.dispatch({ effects: setRemoteCarets.of([caret({ anchor: 2, head: 7 })]) });
    // One mark for the selection, one widget for the caret.
    expect(decorationCount(view)).toBe(2);
    view.destroy();
  });

  it("draws only a caret when nothing is selected", () => {
    const view = editor("hello world");
    view.dispatch({ effects: setRemoteCarets.of([caret({ anchor: 4, head: 4 })]) });
    expect(decorationCount(view)).toBe(1);
    view.destroy();
  });

  it("clamps an offset past the end instead of throwing", () => {
    // A peer a keystroke ahead names a position this document does not have
    // yet, and a decoration past the end takes the editor down with it.
    const view = editor("short");
    expect(() =>
      view.dispatch({ effects: setRemoteCarets.of([caret({ anchor: 900, head: 999 })]) }),
    ).not.toThrow();
    expect(decorationCount(view)).toBe(1);
    view.destroy();
  });

  it("moves a caret along with the text it sits in", () => {
    const view = editor("hello world");
    view.dispatch({ effects: setRemoteCarets.of([caret({ anchor: 6, head: 6 })]) });
    // Typing above somebody must not leave their caret behind.
    view.dispatch({ changes: { from: 0, insert: "XXX" } });
    let position = -1;
    const set = view.state.facet(EditorView.decorations);
    for (const source of set) {
      const decorations = typeof source === "function" ? source(view) : source;
      const cursor = decorations.iter();
      while (cursor.value) {
        position = cursor.from;
        cursor.next();
      }
    }
    expect(position).toBe(9);
    view.destroy();
  });

  it("clears what it was drawing when the list empties", () => {
    const view = editor("hello world");
    view.dispatch({ effects: setRemoteCarets.of([caret({ anchor: 1, head: 3 })]) });
    view.dispatch({ effects: setRemoteCarets.of([]) });
    expect(decorationCount(view)).toBe(0);
    view.destroy();
  });
});
