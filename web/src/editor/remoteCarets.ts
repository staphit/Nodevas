/**
 * Other people's carets, drawn in the editor [P2].
 *
 * Positions arrive over the socket as plain document offsets and are pushed in
 * from React with `setRemoteCarets`. Nothing here reaches back out: the editor
 * only draws, which is what keeps this usable both now, over a document that is
 * saved whole, and later over a CRDT that edits it in place.
 *
 * Offsets come from another browser and may name a place this document does not
 * have — the peer is a keystroke ahead, or on a version this client has not
 * loaded yet. Every one is clamped to the document rather than trusted, because
 * a decoration past the end throws and takes the editor with it.
 */

import { EditorView, Decoration, WidgetType, type DecorationSet } from "@codemirror/view";
import { StateEffect, StateField, type Extension } from "@codemirror/state";

export interface RemoteCaret {
  peerId: string;
  name: string;
  color: string;
  anchor: number;
  head: number;
}

export const setRemoteCarets = StateEffect.define<RemoteCaret[]>();

class CaretWidget extends WidgetType {
  constructor(
    private readonly name: string,
    private readonly color: string,
  ) {
    super();
  }

  // Two carets from the same person at the same place are the same widget, so
  // a redraw on every keystroke does not rebuild the DOM under them.
  eq(other: CaretWidget) {
    return other.name === this.name && other.color === this.color;
  }

  toDOM() {
    const caret = document.createElement("span");
    caret.className = "cm-remote-caret";
    caret.style.setProperty("--peer-color", this.color);
    const label = document.createElement("span");
    label.className = "cm-remote-caret-label";
    label.textContent = this.name;
    caret.appendChild(label);
    return caret;
  }

  ignoreEvent() {
    return true;
  }
}

function decorationsFor(carets: RemoteCaret[], docLength: number): DecorationSet {
  const ranges = [];
  for (const caret of carets) {
    const head = Math.max(0, Math.min(docLength, caret.head));
    const anchor = Math.max(0, Math.min(docLength, caret.anchor));
    const from = Math.min(anchor, head);
    const to = Math.max(anchor, head);
    if (from !== to) {
      ranges.push(
        Decoration.mark({
          class: "cm-remote-selection",
          attributes: { style: `--peer-color: ${caret.color}` },
        }).range(from, to),
      );
    }
    ranges.push(
      Decoration.widget({
        widget: new CaretWidget(caret.name, caret.color),
        side: 1,
      }).range(head),
    );
  }
  // Decoration.set sorts for us; the carets arrive in whatever order the peers
  // did.
  return Decoration.set(ranges, true);
}

const remoteCaretField = StateField.define<DecorationSet>({
  create() {
    return Decoration.none;
  },
  update(carets, transaction) {
    for (const effect of transaction.effects) {
      if (effect.is(setRemoteCarets)) {
        return decorationsFor(effect.value, transaction.state.doc.length);
      }
    }
    // Between updates the marks travel with the text they sit in, so typing
    // above somebody does not leave their caret behind.
    return carets.map(transaction.changes);
  },
  provide: (field) => EditorView.decorations.from(field),
});

const remoteCaretTheme = EditorView.baseTheme({
  ".cm-remote-selection": {
    backgroundColor: "color-mix(in srgb, var(--peer-color) 24%, transparent)",
  },
  ".cm-remote-caret": {
    position: "relative",
    borderLeft: "2px solid var(--peer-color)",
    marginLeft: "-1px",
    // Zero width: it sits between characters rather than taking a column.
    display: "inline-block",
    height: "1.1em",
    verticalAlign: "text-bottom",
  },
  ".cm-remote-caret-label": {
    position: "absolute",
    top: "-1.35em",
    left: "-2px",
    padding: "0 4px",
    borderRadius: "3px",
    background: "var(--peer-color)",
    color: "#fff",
    fontSize: "10px",
    lineHeight: "1.4",
    whiteSpace: "nowrap",
    // The label is a hint, not an obstacle: it must never eat a click meant
    // for the text underneath it.
    pointerEvents: "none",
    opacity: "0.9",
  },
});

export function remoteCarets(): Extension {
  return [remoteCaretField, remoteCaretTheme];
}
