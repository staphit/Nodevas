import { useId } from "react";

import {
  insertColumn,
  insertRow,
  moveColumn,
  moveRow,
  removeColumn,
  removeRow,
  setColumnAlign,
  type TableAlign,
} from "../../editor/table";
import type { TableEditing } from "./useTableEditing";

const TABLE_ALIGNS: readonly (readonly [TableAlign, string])[] = [
  ["none", "預設"],
  ["left", "靠左"],
  ["center", "置中"],
  ["right", "靠右"],
];

/** The second toolbar row, shown only while the caret sits inside a table. */
export function TableToolbar({ tables }: { tables: TableEditing }) {
  const fieldID = useId();
  const { tableAt, applyTableEdit } = tables;
  if (!tableAt) return null;

  return (
    <div
      className="editor-toolbar editor-table-toolbar"
      role="toolbar"
      aria-label="表格編輯"
    >
      <span className="editor-table-where">
        表格 {tableAt.rows.length}×{tableAt.align.length}・第{" "}
        {tableAt.row + 1} 列第 {tableAt.column + 1} 欄
      </span>
      <span className="editor-tool-more-label">列</span>
      <button
        type="button"
        className="tool-btn"
        title="在上方插入一列"
        aria-label="在上方插入一列"
        onClick={() => applyTableEdit((info) => insertRow(info, -1))}
      >
        ＋↑
      </button>
      <button
        type="button"
        className="tool-btn"
        title="在下方插入一列"
        aria-label="在下方插入一列"
        onClick={() => applyTableEdit((info) => insertRow(info, 1))}
      >
        ＋↓
      </button>
      <button
        type="button"
        className="tool-btn"
        title="上移這一列"
        aria-label="上移這一列"
        disabled={tableAt.row <= 1}
        onClick={() => applyTableEdit((info) => moveRow(info, -1))}
      >
        ↑
      </button>
      <button
        type="button"
        className="tool-btn"
        title="下移這一列"
        aria-label="下移這一列"
        disabled={tableAt.row === 0 || tableAt.row >= tableAt.rows.length - 1}
        onClick={() => applyTableEdit((info) => moveRow(info, 1))}
      >
        ↓
      </button>
      <button
        type="button"
        className="tool-btn danger"
        title={tableAt.row === 0 ? "標題列無法刪除" : "刪除這一列"}
        disabled={tableAt.row === 0}
        onClick={() => applyTableEdit(removeRow)}
      >
        ✕
      </button>
      <span className="tool-sep" />
      <span className="editor-tool-more-label">欄</span>
      <button
        type="button"
        className="tool-btn"
        title="在左方插入一欄"
        aria-label="在左方插入一欄"
        onClick={() => applyTableEdit((info) => insertColumn(info, -1))}
      >
        ＋←
      </button>
      <button
        type="button"
        className="tool-btn"
        title="在右方插入一欄"
        aria-label="在右方插入一欄"
        onClick={() => applyTableEdit((info) => insertColumn(info, 1))}
      >
        ＋→
      </button>
      <button
        type="button"
        className="tool-btn"
        title="左移這一欄"
        aria-label="左移這一欄"
        disabled={tableAt.column === 0}
        onClick={() => applyTableEdit((info) => moveColumn(info, -1))}
      >
        ←
      </button>
      <button
        type="button"
        className="tool-btn"
        title="右移這一欄"
        aria-label="右移這一欄"
        disabled={tableAt.column >= tableAt.align.length - 1}
        onClick={() => applyTableEdit((info) => moveColumn(info, 1))}
      >
        →
      </button>
      <button
        type="button"
        className="tool-btn danger"
        title="刪除這一欄"
        aria-label="刪除這一欄"
        disabled={tableAt.align.length <= 1}
        onClick={() => applyTableEdit(removeColumn)}
      >
        ✕
      </button>
      <span className="tool-sep" />
      <label className="editor-tool-more-label" htmlFor={`${fieldID}-table-align`}>
        對齊
      </label>
      <select
        id={`${fieldID}-table-align`}
        className="tool-list-select"
        value={tableAt.align[tableAt.column] ?? "none"}
        onChange={(event) =>
          applyTableEdit((info) =>
            setColumnAlign(info, event.target.value as TableAlign),
          )
        }
      >
        {TABLE_ALIGNS.map(([align, label]) => (
          <option key={align} value={align}>
            {label}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="tool-btn"
        title="重新對齊表格欄寬"
        aria-label="重新對齊表格欄寬"
        onClick={() => applyTableEdit((info) => info)}
      >
        整理
      </button>
    </div>
  );
}
