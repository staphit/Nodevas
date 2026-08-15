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
import { useI18n } from "../../i18n";

const TABLE_ALIGNS: readonly (readonly [TableAlign, string])[] = [
  ["none", "none"],
  ["left", "left"],
  ["center", "center"],
  ["right", "right"],
];

/** The second toolbar row, shown only while the caret sits inside a table. */
export function TableToolbar({ tables }: { tables: TableEditing }) {
  const { t } = useI18n();
  const fieldID = useId();
  const { tableAt, applyTableEdit } = tables;
  if (!tableAt) return null;

  return (
    <div
      className="editor-toolbar editor-table-toolbar"
      role="toolbar"
      aria-label={t("table.aria")}
    >
      <span className="editor-table-where">
        {t("table.position", {
          rows: String(tableAt.rows.length),
          columns: String(tableAt.align.length),
          row: String(tableAt.row + 1),
          column: String(tableAt.column + 1),
        })}
      </span>
      <span className="editor-tool-more-label">{t("table.rows")}</span>
      <button
        type="button"
        className="tool-btn"
        title={t("table.insertRowAbove")}
        aria-label={t("table.insertRowAbove")}
        onClick={() => applyTableEdit((info) => insertRow(info, -1))}
      >
        ＋↑
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.insertRowBelow")}
        aria-label={t("table.insertRowBelow")}
        onClick={() => applyTableEdit((info) => insertRow(info, 1))}
      >
        ＋↓
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.moveRowUp")}
        aria-label={t("table.moveRowUp")}
        disabled={tableAt.row <= 1}
        onClick={() => applyTableEdit((info) => moveRow(info, -1))}
      >
        ↑
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.moveRowDown")}
        aria-label={t("table.moveRowDown")}
        disabled={tableAt.row === 0 || tableAt.row >= tableAt.rows.length - 1}
        onClick={() => applyTableEdit((info) => moveRow(info, 1))}
      >
        ↓
      </button>
      <button
        type="button"
        className="tool-btn danger"
        title={tableAt.row === 0 ? t("table.headerDelete") : t("table.deleteRow")}
        disabled={tableAt.row === 0}
        onClick={() => applyTableEdit(removeRow)}
      >
        ✕
      </button>
      <span className="tool-sep" />
      <span className="editor-tool-more-label">{t("table.columns")}</span>
      <button
        type="button"
        className="tool-btn"
        title={t("table.insertColumnLeft")}
        aria-label={t("table.insertColumnLeft")}
        onClick={() => applyTableEdit((info) => insertColumn(info, -1))}
      >
        ＋←
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.insertColumnRight")}
        aria-label={t("table.insertColumnRight")}
        onClick={() => applyTableEdit((info) => insertColumn(info, 1))}
      >
        ＋→
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.moveColumnLeft")}
        aria-label={t("table.moveColumnLeft")}
        disabled={tableAt.column === 0}
        onClick={() => applyTableEdit((info) => moveColumn(info, -1))}
      >
        ←
      </button>
      <button
        type="button"
        className="tool-btn"
        title={t("table.moveColumnRight")}
        aria-label={t("table.moveColumnRight")}
        disabled={tableAt.column >= tableAt.align.length - 1}
        onClick={() => applyTableEdit((info) => moveColumn(info, 1))}
      >
        →
      </button>
      <button
        type="button"
        className="tool-btn danger"
        title={t("table.deleteColumn")}
        aria-label={t("table.deleteColumn")}
        disabled={tableAt.align.length <= 1}
        onClick={() => applyTableEdit(removeColumn)}
      >
        ✕
      </button>
      <span className="tool-sep" />
      <label className="editor-tool-more-label" htmlFor={`${fieldID}-table-align`}>
        {t("table.align")}
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
            {t(`table.align.${label}`)}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="tool-btn"
        title={t("table.realign")}
        aria-label={t("table.realign")}
        onClick={() => applyTableEdit((info) => info)}
      >
        {t("table.realignLabel")}
      </button>
    </div>
  );
}
