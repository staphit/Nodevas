import type { EditorView } from "@codemirror/view";

import { formatNodeLink } from "../../domain/graph/nodeLink";
import { translate } from "../../i18n";
import type { NodeLinkTarget } from "../NodeLinkPicker";

export type ListStyle =
  | "dash"
  | "star"
  | "plus"
  | "number"
  | "alpha-upper"
  | "alpha-lower"
  | "roman-upper"
  | "roman-lower"
  | "check";

function alphaMarker(index: number): string {
  let value = index + 1;
  let marker = "";
  while (value > 0) {
    value--;
    marker = String.fromCharCode(65 + (value % 26)) + marker;
    value = Math.floor(value / 26);
  }
  return marker;
}

function romanMarker(index: number): string {
  let value = index + 1;
  let marker = "";
  const numerals: [number, string][] = [
    [1000, "M"], [900, "CM"], [500, "D"], [400, "CD"],
    [100, "C"], [90, "XC"], [50, "L"], [40, "XL"],
    [10, "X"], [9, "IX"], [5, "V"], [4, "IV"], [1, "I"],
  ];
  for (const [amount, numeral] of numerals) {
    while (value >= amount) {
      marker += numeral;
      value -= amount;
    }
  }
  return marker;
}

function listMarker(style: ListStyle, index: number): string {
  switch (style) {
    case "dash": return "- ";
    case "star": return "* ";
    case "plus": return "+ ";
    case "number": return `${index + 1}. `;
    case "alpha-upper": return `${alphaMarker(index)}. `;
    case "alpha-lower": return `${alphaMarker(index).toLowerCase()}. `;
    case "roman-upper": return `${romanMarker(index)}. `;
    case "roman-lower": return `${romanMarker(index).toLowerCase()}. `;
    case "check": return "- [ ] ";
  }
}

export function wrapSelection(
  v: EditorView | undefined,
  before: string,
  after: string,
  placeholder = translate("editor.textPlaceholder"),
) {
  if (!v) return;
  const { from, to } = v.state.selection.main;
  const sel = v.state.sliceDoc(from, to) || placeholder;
  v.dispatch({
    changes: { from, to, insert: `${before}${sel}${after}` },
    selection: { anchor: from + before.length, head: from + before.length + sel.length },
  });
  v.focus();
}

/** Inserts a `[[…]]` link for the picked node at the caret. */
export function insertNodeLink(
  v: EditorView | undefined,
  target: NodeLinkTarget,
  currentProject: string,
) {
  if (!v) return;
  const { from, to } = v.state.selection.main;
  const selected = v.state.sliceDoc(from, to).trim();
  const text = formatNodeLink({
    project: target.project,
    nodeId: target.nodeId,
    label: selected || target.title,
    currentProject,
  });
  v.dispatch({
    changes: { from, to, insert: text },
    selection: { anchor: from + text.length },
  });
  v.focus();
}

export function setHeading(v: EditorView | undefined, level: number) {
  if (!v) return;
  const line = v.state.doc.lineAt(v.state.selection.main.head);
  const cleaned = line.text.replace(/^#{1,6}\s+/, "");
  const prefix = "#".repeat(level) + " ";
  const already = line.text.startsWith(prefix) && !line.text.startsWith(prefix + "#");
  const next = already ? cleaned : prefix + cleaned;
  v.dispatch({ changes: { from: line.from, to: line.to, insert: next } });
  v.focus();
}

export function prefixLines(v: EditorView | undefined, prefix: string) {
  if (!v) return;
  const { from, to } = v.state.selection.main;
  const startLine = v.state.doc.lineAt(from);
  const endLine = v.state.doc.lineAt(to);
  const changes = [];
  for (let n = startLine.number; n <= endLine.number; n++) {
    const l = v.state.doc.line(n);
    changes.push({ from: l.from, insert: prefix });
  }
  v.dispatch({ changes });
  v.focus();
}

export function applyListStyle(v: EditorView | undefined, style: ListStyle) {
  if (!v) return;
  const { from, to } = v.state.selection.main;
  const startLine = v.state.doc.lineAt(from);
  let endLine = v.state.doc.lineAt(to);
  if (to > from && to === endLine.from) endLine = v.state.doc.line(endLine.number - 1);
  const changes: { from: number; to: number; insert: string }[] = [];
  for (let n = startLine.number; n <= endLine.number; n++) {
    const line = v.state.doc.line(n);
    const indent = line.text.match(/^\s*/)?.[0] ?? "";
    const content = line.text
      .slice(indent.length)
      .replace(/^(?:[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+|[A-Za-z]+[.)]\s+)/, "");
    changes.push({
      from: line.from,
      to: line.to,
      insert: `${indent}${listMarker(style, n - startLine.number)}${content}`,
    });
  }
  v.dispatch({ changes });
  v.focus();
}

export function insertSnippet(v: EditorView | undefined, snippet: string) {
  if (!v) return;
  const { from, to } = v.state.selection.main;
  v.dispatch({
    changes: { from, to, insert: snippet },
    selection: { anchor: from + snippet.length },
  });
  v.focus();
}
