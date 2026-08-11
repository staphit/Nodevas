import { useCallback, useRef, useState, type RefObject } from "react";
import type { EditorView } from "@codemirror/view";
import type { EditorState } from "@codemirror/state";

import {
  buildTable,
  cellOffset,
  findTableAt,
  renderTable,
  rowLineOffset,
  type LineSource,
  type TableInfo,
} from "../../editor/table";

/** Reads document lines straight out of CodeMirror, without copying the doc. */
function lineSourceOf(state: EditorState): LineSource {
  return {
    count: state.doc.lines,
    line: (index) => state.doc.line(index + 1).text,
  };
}

export type TableEditing = {
  tableAt: TableInfo | null;
  tableRows: number;
  setTableRows: (rows: number) => void;
  tableColumns: number;
  setTableColumns: (columns: number) => void;
  tableMenuRef: RefObject<HTMLDetailsElement>;
  syncTable: (state: EditorState) => void;
  insertTable: (rows: number, columns: number) => void;
  applyTableEdit: (mutate: (info: TableInfo) => TableInfo | null) => void;
};

// The document is Markdown, so a table is text: every structure edit
// re-renders the whole block and puts the caret back in the same cell.
export function useTableEditing(view: () => EditorView | undefined): TableEditing {
  const [tableAt, setTableAt] = useState<TableInfo | null>(null);
  const [tableRows, setTableRows] = useState(3);
  const [tableColumns, setTableColumns] = useState(3);
  const tableMenuRef = useRef<HTMLDetailsElement>(null);

  const syncTable = useCallback((state: EditorState) => {
    const head = state.selection.main.head;
    const line = state.doc.lineAt(head);
    setTableAt(
      findTableAt(lineSourceOf(state), line.number - 1, head - line.from),
    );
  }, []);

  const insertTable = useCallback((rows: number, columns: number) => {
    const v = view();
    if (!v) return;
    const line = v.state.doc.lineAt(v.state.selection.main.head);
    const table = buildTable(rows, columns);
    const onBlankLine = line.text.trim() === "";
    const insert = (onBlankLine ? "" : "\n\n") + table + "\n";
    const from = onBlankLine ? line.from : line.to;
    const start = from + (onBlankLine ? 0 : 2);
    v.dispatch({
      changes: { from, to: line.to, insert },
      selection: { anchor: start + cellOffset(table.split("\n")[0], 0) },
    });
    v.focus();
    tableMenuRef.current?.removeAttribute("open");
  }, []);

  const applyTableEdit = useCallback(
    (mutate: (info: TableInfo) => TableInfo | null) => {
      const v = view();
      if (!v || !tableAt) return;
      const next = mutate(tableAt);
      if (!next) return;
      const text = renderTable(next.rows, next.align);
      const from = v.state.doc.line(next.startLine + 1).from;
      const to = v.state.doc.line(
        Math.min(next.endLine + 1, v.state.doc.lines),
      ).to;
      const lines = text.split("\n");
      const row = Math.min(rowLineOffset(next.row), lines.length - 1);
      const anchor =
        from +
        lines.slice(0, row).reduce((sum, line) => sum + line.length + 1, 0) +
        cellOffset(lines[row], next.column);
      v.dispatch({ changes: { from, to, insert: text }, selection: { anchor } });
      v.focus();
    },
    [tableAt],
  );

  return {
    tableAt,
    tableRows,
    setTableRows,
    tableColumns,
    setTableColumns,
    tableMenuRef,
    syncTable,
    insertTable,
    applyTableEdit,
  };
}
