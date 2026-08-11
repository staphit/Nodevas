/**
 * Markdown pipe-table editing.
 *
 * The editor stores plain Markdown, so a table is just text — every operation
 * here parses the table around the caret, changes its structure, and renders
 * the whole block back with padded columns. Pure functions, no CodeMirror, so
 * the behaviour is testable on its own.
 */

export type TableAlign = "none" | "left" | "center" | "right";

/** Random access over document lines, so callers need not split the doc. */
export interface LineSource {
  count: number;
  line: (index: number) => string;
}

export interface TableInfo {
  /** 0-based inclusive line range the table occupies. */
  startLine: number;
  endLine: number;
  align: TableAlign[];
  /** Header row first; the delimiter row is not part of this list. */
  rows: string[][];
  /** Caret position: row index into `rows`, and column index. */
  row: number;
  column: number;
}

const MIN_COLUMN_WIDTH = 3;

export function linesOf(text: string): LineSource {
  const lines = text.split("\n");
  return { count: lines.length, line: (index) => lines[index] ?? "" };
}

/** Column count of a fullwidth-aware monospaced render. */
export function displayWidth(text: string): number {
  let width = 0;
  for (const char of text) {
    const code = char.codePointAt(0) ?? 0;
    width +=
      (code >= 0x1100 && code <= 0x115f) ||
      (code >= 0x2e80 && code <= 0x303e) ||
      (code >= 0x3041 && code <= 0x33ff) ||
      (code >= 0x3400 && code <= 0x4dbf) ||
      (code >= 0x4e00 && code <= 0x9fff) ||
      (code >= 0xa000 && code <= 0xa4cf) ||
      (code >= 0xac00 && code <= 0xd7a3) ||
      (code >= 0xf900 && code <= 0xfaff) ||
      (code >= 0xfe30 && code <= 0xfe6f) ||
      (code >= 0xff00 && code <= 0xff60) ||
      (code >= 0xffe0 && code <= 0xffe6) ||
      (code >= 0x1f300 && code <= 0x1f9ff) ||
      (code >= 0x20000 && code <= 0x3fffd)
        ? 2
        : 1;
  }
  return width;
}

/** A row line: has at least one unescaped pipe and some content. */
export function isTableRow(line: string): boolean {
  if (line.trim() === "") return false;
  for (let index = 0; index < line.length; index++) {
    if (line[index] === "\\") {
      index++;
      continue;
    }
    if (line[index] === "|") return true;
  }
  return false;
}

export function isDelimiterRow(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed.includes("-")) return false;
  return /^\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?$/.test(trimmed);
}

/** Splits a row into cell texts, resolving `\|` escapes. */
export function splitRow(line: string): string[] {
  let text = line.trim();
  if (text.startsWith("|")) text = text.slice(1);
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.slice(0, -1);
  const cells: string[] = [];
  let buffer = "";
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === "\\" && text[index + 1] === "|") {
      buffer += "|";
      index++;
      continue;
    }
    if (char === "|") {
      cells.push(buffer.trim());
      buffer = "";
      continue;
    }
    buffer += char;
  }
  cells.push(buffer.trim());
  return cells;
}

function parseAlign(cell: string): TableAlign {
  const trimmed = cell.trim();
  const left = trimmed.startsWith(":");
  const right = trimmed.endsWith(":");
  if (left && right) return "center";
  if (right) return "right";
  if (left) return "left";
  return "none";
}

/** Which cell the caret sits in, counting unescaped pipes before it. */
export function columnAtOffset(line: string, offset: number): number {
  let text = line;
  let start = 0;
  if (text.trimStart().startsWith("|")) {
    start = text.indexOf("|") + 1;
  }
  let column = 0;
  for (let index = start; index < Math.min(offset, text.length); index++) {
    if (text[index] === "\\") {
      index++;
      continue;
    }
    if (text[index] === "|") column++;
  }
  return column;
}

/**
 * Finds the table containing `index`, or null. A table is a run of row lines
 * whose second line is the alignment delimiter. `offsetInLine` is the caret's
 * character offset inside that line and decides the current column.
 */
export function findTableAt(
  source: LineSource,
  index: number,
  offsetInLine = 0,
): TableInfo | null {
  if (index < 0 || index >= source.count) return null;
  if (!isTableRow(source.line(index))) return null;
  let start = index;
  while (start > 0 && isTableRow(source.line(start - 1))) start--;
  let end = index;
  while (end + 1 < source.count && isTableRow(source.line(end + 1))) end++;
  if (end - start < 1 || !isDelimiterRow(source.line(start + 1))) return null;

  const align = splitRow(source.line(start + 1)).map(parseAlign);
  const rows: string[][] = [];
  let caretRow = 0;
  for (let line = start; line <= end; line++) {
    if (line === start + 1) {
      // The caret sitting on the delimiter edits the header.
      if (index === line) caretRow = 0;
      continue;
    }
    if (index === line) caretRow = rows.length;
    rows.push(splitRow(source.line(line)));
  }
  const columns = Math.max(align.length, ...rows.map((row) => row.length));
  const normalized = rows.map((row) => fitRow(row, columns));
  const column = Math.min(
    Math.max(0, columnAtOffset(source.line(index), offsetInLine)),
    columns - 1,
  );
  return {
    startLine: start,
    endLine: end,
    align: fitAlign(align, columns),
    rows: normalized,
    row: caretRow,
    column,
  };
}

function fitRow(row: string[], columns: number): string[] {
  const next = row.slice(0, columns);
  while (next.length < columns) next.push("");
  return next;
}

function fitAlign(align: TableAlign[], columns: number): TableAlign[] {
  const next = align.slice(0, columns);
  while (next.length < columns) next.push("none");
  return next;
}

function escapeCell(text: string): string {
  return text.replace(/\|/g, "\\|").replace(/\n/g, " ");
}

function pad(text: string, width: number, align: TableAlign): string {
  const gap = Math.max(0, width - displayWidth(text));
  if (gap === 0) return text;
  if (align === "right") return " ".repeat(gap) + text;
  if (align === "center") {
    const left = Math.floor(gap / 2);
    return " ".repeat(left) + text + " ".repeat(gap - left);
  }
  return text + " ".repeat(gap);
}

function delimiterCell(width: number, align: TableAlign): string {
  const size = Math.max(MIN_COLUMN_WIDTH, width);
  switch (align) {
    case "left":
      return ":" + "-".repeat(size - 1);
    case "right":
      return "-".repeat(size - 1) + ":";
    case "center":
      return ":" + "-".repeat(size - 2) + ":";
    default:
      return "-".repeat(size);
  }
}

/** Renders rows + alignment back to padded Markdown. */
export function renderTable(rows: string[][], align: TableAlign[]): string {
  const columns = Math.max(align.length, ...rows.map((row) => row.length), 1);
  const cells = rows.map((row) => fitRow(row, columns).map(escapeCell));
  const aligns = fitAlign(align, columns);
  const widths: number[] = [];
  for (let column = 0; column < columns; column++) {
    let width = MIN_COLUMN_WIDTH;
    for (const row of cells) width = Math.max(width, displayWidth(row[column]));
    widths.push(width);
  }
  const line = (row: string[]) =>
    "| " + row.map((cell, column) => pad(cell, widths[column], aligns[column])).join(" | ") + " |";
  const out: string[] = [];
  out.push(line(cells[0] ?? new Array(columns).fill("")));
  out.push(
    "| " + widths.map((width, column) => delimiterCell(width, aligns[column])).join(" | ") + " |",
  );
  for (const row of cells.slice(1)) out.push(line(row));
  return out.join("\n");
}

/**
 * Character offset of the start of a cell's text, used to drop the caret into
 * the right cell after a structure edit.
 */
export function cellOffset(line: string, column: number): number {
  let seen = -1;
  for (let index = 0; index < line.length; index++) {
    if (line[index] === "\\") {
      index++;
      continue;
    }
    if (line[index] !== "|") continue;
    seen++;
    if (seen === column) return Math.min(index + 2, line.length);
  }
  return line.length;
}

/** Document line offset of a row inside a rendered table (0 = header). */
export function rowLineOffset(row: number): number {
  return row <= 0 ? 0 : row + 1;
}

/** Renders a fresh empty table with header placeholders. */
export function buildTable(rowCount: number, columnCount: number): string {
  const columns = Math.max(1, Math.min(20, Math.trunc(columnCount)));
  const bodyRows = Math.max(1, Math.min(100, Math.trunc(rowCount)));
  const header = Array.from({ length: columns }, (_, index) => `欄 ${index + 1}`);
  const rows = [header];
  for (let row = 0; row < bodyRows; row++) rows.push(new Array(columns).fill(""));
  return renderTable(rows, new Array(columns).fill("none"));
}

/** Re-pads the table without changing its content. */
export function formatTable(info: TableInfo): string {
  return renderTable(info.rows, info.align);
}

function clone(info: TableInfo): TableInfo {
  return {
    ...info,
    align: [...info.align],
    rows: info.rows.map((row) => [...row]),
  };
}

/** Inserts an empty row above (-1) or below (1) the caret row. */
export function insertRow(info: TableInfo, offset: -1 | 1): TableInfo {
  const next = clone(info);
  const columns = next.align.length;
  const at = Math.max(1, offset < 0 ? next.row : next.row + 1);
  next.rows.splice(at, 0, new Array(columns).fill(""));
  next.row = at;
  return next;
}

/** Removes the caret row. The header row cannot be removed. */
export function removeRow(info: TableInfo): TableInfo | null {
  if (info.row <= 0 || info.rows.length <= 1) return null;
  const next = clone(info);
  next.rows.splice(next.row, 1);
  next.row = Math.min(next.row, next.rows.length - 1);
  return next;
}

/** Moves the caret row up (-1) or down (1) within the body. */
export function moveRow(info: TableInfo, offset: -1 | 1): TableInfo | null {
  const target = info.row + offset;
  if (info.row <= 0 || target <= 0 || target >= info.rows.length) return null;
  const next = clone(info);
  const [row] = next.rows.splice(next.row, 1);
  next.rows.splice(target, 0, row);
  next.row = target;
  return next;
}

/** Inserts an empty column left (-1) or right (1) of the caret column. */
export function insertColumn(info: TableInfo, offset: -1 | 1): TableInfo {
  const next = clone(info);
  const at = Math.max(0, Math.min(next.align.length, offset < 0 ? next.column : next.column + 1));
  next.align.splice(at, 0, "none");
  next.rows = next.rows.map((row) => {
    const copy = [...row];
    copy.splice(at, 0, "");
    return copy;
  });
  next.column = at;
  return next;
}

/** Removes the caret column; the last remaining column is kept. */
export function removeColumn(info: TableInfo): TableInfo | null {
  if (info.align.length <= 1) return null;
  const next = clone(info);
  const at = Math.min(next.column, next.align.length - 1);
  next.align.splice(at, 1);
  next.rows = next.rows.map((row) => {
    const copy = [...row];
    copy.splice(at, 1);
    return copy;
  });
  next.column = Math.min(at, next.align.length - 1);
  return next;
}

/** Moves the caret column left (-1) or right (1). */
export function moveColumn(info: TableInfo, offset: -1 | 1): TableInfo | null {
  const target = info.column + offset;
  if (target < 0 || target >= info.align.length) return null;
  const next = clone(info);
  const [align] = next.align.splice(next.column, 1);
  next.align.splice(target, 0, align);
  next.rows = next.rows.map((row) => {
    const copy = [...row];
    const [cell] = copy.splice(info.column, 1);
    copy.splice(target, 0, cell);
    return copy;
  });
  next.column = target;
  return next;
}

/** Sets the alignment of the caret column. */
export function setColumnAlign(info: TableInfo, align: TableAlign): TableInfo {
  const next = clone(info);
  const at = Math.min(next.column, next.align.length - 1);
  next.align[at] = align;
  return next;
}
