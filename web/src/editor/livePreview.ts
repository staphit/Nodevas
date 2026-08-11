/**
 * Live markdown preview for CodeMirror.
 *
 * The document stays plain markdown — that is the file on disk, and the whole
 * persistence design depends on it. What changes is what you *see*: syntax
 * markers are hidden and the text is styled, so editing looks like the rendered
 * result instead of source code.
 *
 * The line the cursor is on always shows its raw markers, which is what makes
 * the mode editable rather than a preview: to change `**bold**` you put the
 * caret in it and the asterisks come back.
 */

import { syntaxTree } from "@codemirror/language";
import {
  nodeLinkPattern,
  splitLinkTarget,
} from "../domain/graph/nodeLink";
import {
  RangeSetBuilder,
  StateField,
  type EditorState,
  type Extension,
  type Range,
} from "@codemirror/state";
import {
  Decoration,
  EditorView,
  ViewPlugin,
  WidgetType,
  type DecorationSet,
  type ViewUpdate,
} from "@codemirror/view";

/** Follows a node link; see {@link livePreview}. */
export type NodeLinkHandler = (target: { project: string; nodeId: string }) => void;

/** Marker node names whose text is hidden while the line is not being edited. */
const HIDDEN_MARKS = new Set([
  "HeaderMark",
  "EmphasisMark",
  "StrikethroughMark",
  "CodeMark",
  "QuoteMark",
  "LinkMark",
  "URL",
  "LinkTitle",
]);

const HEADING_LINE: Record<string, string> = {
  ATXHeading1: "cm-live-h1",
  ATXHeading2: "cm-live-h2",
  ATXHeading3: "cm-live-h3",
  ATXHeading4: "cm-live-h4",
  ATXHeading5: "cm-live-h5",
  ATXHeading6: "cm-live-h6",
  SetextHeading1: "cm-live-h1",
  SetextHeading2: "cm-live-h2",
};

class BulletWidget extends WidgetType {
  constructor(private readonly marker: string) {
    super();
  }
  eq(other: BulletWidget) {
    return other.marker === this.marker;
  }
  toDOM() {
    const span = document.createElement("span");
    span.className = "cm-live-bullet";
    span.textContent = this.marker;
    return span;
  }
}

class TaskWidget extends WidgetType {
  constructor(
    private readonly checked: boolean,
    private readonly from: number,
    private readonly to: number,
  ) {
    super();
  }
  eq(other: TaskWidget) {
    return other.checked === this.checked && other.from === this.from;
  }
  toDOM(view: EditorView) {
    const box = document.createElement("input");
    box.type = "checkbox";
    box.className = "cm-live-task";
    box.checked = this.checked;
    box.setAttribute("aria-label", this.checked ? "已完成" : "未完成");
    box.addEventListener("mousedown", (event) => event.preventDefault());
    box.addEventListener("click", () => {
      // Toggling writes the markdown back, so the file stays the truth.
      view.dispatch({
        changes: {
          from: this.from,
          to: this.to,
          insert: this.checked ? "[ ]" : "[x]",
        },
      });
    });
    return box;
  }
  ignoreEvent() {
    return false;
  }
}

class ImageWidget extends WidgetType {
  constructor(
    private readonly url: string,
    private readonly alt: string,
  ) {
    super();
  }
  eq(other: ImageWidget) {
    return other.url === this.url && other.alt === this.alt;
  }
  toDOM() {
    const image = document.createElement("img");
    image.className = "cm-live-image";
    image.src = this.url;
    image.alt = this.alt;
    image.loading = "lazy";
    return image;
  }
}

/** Renders a markdown table as a real table while the caret is elsewhere. */
class TableWidget extends WidgetType {
  constructor(private readonly source: string) {
    super();
  }
  eq(other: TableWidget) {
    return other.source === this.source;
  }
  toDOM() {
    const rows = this.source
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean);
    const table = document.createElement("table");
    table.className = "cm-live-table";
    const body = document.createElement("tbody");
    rows.forEach((line, index) => {
      const cells = line
        .replace(/^\||\|$/g, "")
        .split("|")
        .map((cell) => cell.trim());
      // The `---|---` separator is layout, not content.
      if (cells.every((cell) => /^:?-{2,}:?$/.test(cell))) return;
      const row = document.createElement("tr");
      for (const cell of cells) {
        const element = document.createElement(index === 0 ? "th" : "td");
        element.textContent = cell;
        row.appendChild(element);
      }
      body.appendChild(row);
    });
    table.appendChild(body);
    return table;
  }
  ignoreEvent() {
    return false;
  }
}

/**
 * A `[[project/node-id|label]]` link, drawn as a link you can click.
 *
 * The click is handled here rather than by the editor because the target is
 * not a URL: it is a node in this workspace, and following it may switch
 * project first.
 */
class NodeLinkWidget extends WidgetType {
  constructor(
    private readonly project: string,
    private readonly nodeId: string,
    private readonly label: string,
    private readonly onOpen?: NodeLinkHandler,
  ) {
    super();
  }
  eq(other: NodeLinkWidget) {
    return (
      other.project === this.project &&
      other.nodeId === this.nodeId &&
      other.label === this.label
    );
  }
  toDOM() {
    const anchor = document.createElement("a");
    anchor.className = "cm-live-node-link";
    anchor.textContent = this.label;
    anchor.title = this.project
      ? `${this.project} / ${this.nodeId}`
      : this.nodeId;
    anchor.addEventListener("mousedown", (event) => event.preventDefault());
    anchor.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      this.onOpen?.({ project: this.project, nodeId: this.nodeId });
    });
    return anchor;
  }
  ignoreEvent() {
    return true;
  }
}

class RuleWidget extends WidgetType {
  eq() {
    return true;
  }
  toDOM() {
    const rule = document.createElement("hr");
    rule.className = "cm-live-rule";
    return rule;
  }
}

/** Lines touched by a cursor or selection keep their markers visible. */
function editedLines(view: EditorView): Set<number> {
  const lines = new Set<number>();
  for (const range of view.state.selection.ranges) {
    const first = view.state.doc.lineAt(range.from).number;
    const last = view.state.doc.lineAt(range.to).number;
    for (let line = first; line <= last; line++) lines.add(line);
  }
  return lines;
}

function decorate(view: EditorView, onOpen?: NodeLinkHandler): DecorationSet {
  const active = editedLines(view);
  const marks: Range<Decoration>[] = [];
  const lineClasses = new Map<number, Set<string>>();
  const doc = view.state.doc;

  const addLineClass = (pos: number, className: string) => {
    const line = doc.lineAt(pos);
    const classes = lineClasses.get(line.from) ?? new Set<string>();
    classes.add(className);
    lineClasses.set(line.from, classes);
  };

  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({
      from,
      to,
      enter: (node) => {
        const lineNumber = doc.lineAt(node.from).number;
        const isEditing = active.has(lineNumber);

        const headingClass = HEADING_LINE[node.name];
        if (headingClass) addLineClass(node.from, headingClass);
        if (node.name === "Blockquote") addLineClass(node.from, "cm-live-quote");

        if (node.name === "FencedCode" || node.name === "CodeBlock") {
          // Frame the block whether or not it is being edited: the frame is
          // what makes a code block recognisable at a glance.
          const first = doc.lineAt(node.from).number;
          const last = doc.lineAt(node.to).number;
          for (let line = first; line <= last; line++) {
            addLineClass(doc.line(line).from, "cm-live-code");
          }
          return;
        }


        if (node.name === "HorizontalRule" && !isEditing) {
          marks.push(
            Decoration.replace({ widget: new RuleWidget(), block: false }).range(
              node.from,
              node.to,
            ),
          );
          return;
        }

        if (node.name === "Image" && !isEditing) {
          const text = doc.sliceString(node.from, node.to);
          const match = /^!\[([^\]]*)\]\(([^)\s]+)/.exec(text);
          if (match) {
            marks.push(
              Decoration.replace({
                widget: new ImageWidget(match[2], match[1]),
              }).range(node.from, node.to),
            );
            return;
          }
        }

        if (node.name === "TaskMarker" && !isEditing) {
          const text = doc.sliceString(node.from, node.to);
          marks.push(
            Decoration.replace({
              widget: new TaskWidget(/\[[xX]\]/.test(text), node.from, node.to),
            }).range(node.from, node.to),
          );
          return;
        }

        if (node.name === "ListMark" && !isEditing) {
          const text = doc.sliceString(node.from, node.to);
          // Ordered markers keep their number; bullets become a real bullet.
          const marker = /^\d/.test(text) ? text : "•";
          marks.push(
            Decoration.replace({ widget: new BulletWidget(marker) }).range(
              node.from,
              node.to,
            ),
          );
          return;
        }

        if (!isEditing && HIDDEN_MARKS.has(node.name)) {
          // A link's URL only hides when the text part is there to show.
          if (node.name === "URL" || node.name === "LinkTitle") {
            const parent = node.node.parent;
            if (!parent || parent.name !== "Link") return;
          }
          if (node.to > node.from) {
            marks.push(Decoration.replace({}).range(node.from, node.to));
          }
        }
      },
    });
  }

  // Node links are plain text to the markdown parser, so they are matched on
  // the source rather than read off the syntax tree.
  for (const { from, to } of view.visibleRanges) {
    const text = doc.sliceString(from, to);
    const pattern = nodeLinkPattern();
    for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
      const start = from + match.index;
      const end = start + match[0].length;
      if (active.has(doc.lineAt(start).number)) continue;
      const { project, nodeId } = splitLinkTarget(match[1]);
      if (!nodeId) continue;
      marks.push(
        Decoration.replace({
          widget: new NodeLinkWidget(
            project,
            nodeId,
            (match[2] ?? "").trim() || nodeId,
            onOpen,
          ),
        }).range(start, end),
      );
    }
  }

  const builder = new RangeSetBuilder<Decoration>();
  const all: Range<Decoration>[] = [];
  for (const [linePos, classes] of lineClasses) {
    all.push(
      Decoration.line({ class: [...classes].join(" ") }).range(linePos),
    );
  }
  all.push(...marks);
  all.sort((a, b) => a.from - b.from || a.value.startSide - b.value.startSide);
  for (const range of all) builder.add(range.from, range.to, range.value);
  return builder.finish();
}

/**
 * Tables are rendered as one block widget covering their whole source, so a
 * table looks like a table. Block decorations may only come from a state
 * field, never from a view plugin.
 */
function tableDecorations(state: EditorState): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>();
  const doc = state.doc;
  const selected = state.selection.ranges;
  syntaxTree(state).iterate({
    enter: (node) => {
      if (node.name !== "Table") return;
      const from = doc.lineAt(node.from).from;
      const to = doc.lineAt(node.to).to;
      // Editing inside the table shows its markdown again.
      if (selected.some((range) => range.from <= to && range.to >= from)) return;
      builder.add(
        from,
        to,
        Decoration.replace({
          widget: new TableWidget(doc.sliceString(from, to)),
          block: true,
        }),
      );
    },
  });
  return builder.finish();
}

const liveTables = StateField.define<DecorationSet>({
  create: (state) => tableDecorations(state),
  update: (value, tr) =>
    tr.docChanged || tr.selection ? tableDecorations(tr.state) : value,
  provide: (field) => EditorView.decorations.from(field),
});

function livePreviewPlugin(onOpen?: NodeLinkHandler) {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = decorate(view, onOpen);
      }
      update(update: ViewUpdate) {
        if (update.docChanged || update.selectionSet || update.viewportChanged) {
          this.decorations = decorate(update.view, onOpen);
        }
      }
    },
    {
      decorations: (plugin) => plugin.decorations,
      // Hidden markers are skipped by the caret instead of being stepped through.
      provide: (plugin) =>
        EditorView.atomicRanges.of(
          (view) => view.plugin(plugin)?.decorations ?? Decoration.none,
        ),
    },
  );
}

const livePreviewTheme = EditorView.theme({
  ".cm-live-h1": { fontSize: "1.55em", fontWeight: "700", lineHeight: "1.35" },
  ".cm-live-h2": { fontSize: "1.32em", fontWeight: "700", lineHeight: "1.35" },
  ".cm-live-h3": { fontSize: "1.15em", fontWeight: "600" },
  ".cm-live-h4": { fontWeight: "600" },
  ".cm-live-h5": { fontWeight: "600" },
  ".cm-live-h6": { fontWeight: "600" },
  ".cm-live-quote": {
    borderLeft: "3px solid var(--border-strong, #4a5568)",
    paddingLeft: "10px",
    color: "var(--text-2, #9aa5b1)",
  },
  ".cm-live-bullet": { opacity: "0.75" },
  ".cm-live-task": { marginRight: "4px", verticalAlign: "middle" },
  ".cm-live-image": { maxWidth: "100%", borderRadius: "6px", display: "block" },
  ".cm-live-code": {
    backgroundColor: "var(--surface-2, #1b2127)",
    fontFamily: "var(--mono, ui-monospace, monospace)",
    borderLeft: "3px solid var(--border-strong, #4a5568)",
    paddingLeft: "8px",
  },
  ".cm-live-table": {
    borderCollapse: "collapse",
    margin: "4px 0",
    fontSize: "0.95em",
  },
  ".cm-live-table th, .cm-live-table td": {
    border: "1px solid var(--border, #384049)",
    padding: "3px 8px",
    textAlign: "left",
  },
  ".cm-live-table th": { backgroundColor: "var(--surface-2, #1b2127)" },
  ".cm-live-node-link": {
    color: "var(--accent, #7aa2f7)",
    textDecoration: "none",
    borderBottom: "1px solid color-mix(in srgb, currentColor 45%, transparent)",
    cursor: "pointer",
  },
  ".cm-live-rule": {
    border: "0",
    borderTop: "1px solid var(--border, #384049)",
    margin: "6px 0",
  },
});

/**
 * Renders markdown as you type; the document itself stays markdown.
 *
 * `onOpenNodeLink` is what a `[[…]]` link calls when clicked; without it the
 * links still render, they just do nothing.
 */
export function livePreview(options: { onOpenNodeLink?: NodeLinkHandler } = {}): Extension {
  return [
    liveTables,
    livePreviewPlugin(options.onOpenNodeLink),
    livePreviewTheme,
  ];
}
