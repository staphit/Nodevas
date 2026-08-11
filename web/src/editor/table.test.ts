import { describe, expect, it } from "vitest";

import {
  buildTable,
  cellOffset,
  columnAtOffset,
  findTableAt,
  formatTable,
  insertColumn,
  insertRow,
  linesOf,
  moveColumn,
  moveRow,
  removeColumn,
  removeRow,
  renderTable,
  rowLineOffset,
  setColumnAlign,
  splitRow,
} from "./table";

const doc = [
  "intro",
  "",
  "| Name | Count | Note |",
  "| :--- | ----: | :--: |",
  "| a | 1 | yes |",
  "| b | 22 | no |",
  "",
  "outro",
].join("\n");

function table(line: number, offset = 0) {
  const info = findTableAt(linesOf(doc), line, offset);
  if (!info) throw new Error(`no table at line ${line}`);
  return info;
}

describe("table detection", () => {
  it("finds the table around any of its lines", () => {
    for (const line of [2, 3, 4, 5]) {
      const info = table(line);
      expect(info.startLine).toBe(2);
      expect(info.endLine).toBe(5);
    }
  });

  it("ignores text outside the table", () => {
    const source = linesOf(doc);
    expect(findTableAt(source, 0)).toBeNull();
    expect(findTableAt(source, 1)).toBeNull();
    expect(findTableAt(source, 7)).toBeNull();
  });

  it("needs a delimiter row, not just pipes", () => {
    const source = linesOf("| a | b |\n| c | d |\n");
    expect(findTableAt(source, 0)).toBeNull();
  });

  it("reads cells, alignment and the caret position", () => {
    const info = table(4, 8);
    expect(info.rows).toEqual([
      ["Name", "Count", "Note"],
      ["a", "1", "yes"],
      ["b", "22", "no"],
    ]);
    expect(info.align).toEqual(["left", "right", "center"]);
    expect(info.row).toBe(1);
    expect(info.column).toBe(1);
  });

  it("treats the delimiter row as the header row", () => {
    expect(table(3).row).toBe(0);
  });

  it("keeps escaped pipes inside a cell", () => {
    expect(splitRow(String.raw`| a \| b | c |`)).toEqual(["a | b", "c"]);
    const info = findTableAt(linesOf(String.raw`| a \| b | c |` + "\n| --- | --- |"), 0);
    expect(info?.rows[0]).toEqual(["a | b", "c"]);
    expect(renderTable(info!.rows, info!.align)).toContain(String.raw`a \| b`);
  });

  it("counts columns by unescaped pipes only", () => {
    const line = String.raw`| a \| b | c |`;
    expect(columnAtOffset(line, 4)).toBe(0);
    expect(columnAtOffset(line, 12)).toBe(1);
  });

  it("locates the caret target of a cell", () => {
    const line = "| Name | Count | Note |";
    expect(cellOffset(line, 0)).toBe(2);
    expect(cellOffset(line, 1)).toBe(9);
    expect(line.slice(cellOffset(line, 2))).toBe("Note |");
    expect(rowLineOffset(0)).toBe(0);
    expect(rowLineOffset(1)).toBe(2);
  });
});

describe("table rendering", () => {
  it("pads columns and keeps the alignment markers", () => {
    expect(formatTable(table(4))).toBe(
      [
        "| Name | Count | Note |",
        "| :--- | ----: | :--: |",
        "| a    |     1 | yes  |",
        "| b    |    22 |  no  |",
      ].join("\n"),
    );
  });

  it("pads fullwidth text by display width", () => {
    const source = linesOf("| 名稱 | v |\n| --- | --- |\n| ab | c |");
    const info = findTableAt(source, 0)!;
    expect(formatTable(info)).toBe(
      ["| 名稱 | v   |", "| ---- | --- |", "| ab   | c   |"].join("\n"),
    );
  });

  it("builds an empty table with a header and body rows", () => {
    expect(buildTable(2, 3)).toBe(
      [
        "| 欄 1 | 欄 2 | 欄 3 |",
        "| ---- | ---- | ---- |",
        "|      |      |      |",
        "|      |      |      |",
      ].join("\n"),
    );
  });

  it("clamps absurd sizes", () => {
    expect(buildTable(0, 0).split("\n")).toHaveLength(3);
    expect(buildTable(500, 50).split("\n")).toHaveLength(102);
  });
});

describe("table structure edits", () => {
  it("inserts a row below the caret", () => {
    const next = insertRow(table(4), 1);
    expect(next.rows.map((row) => row[0])).toEqual(["Name", "a", "", "b"]);
    expect(next.row).toBe(2);
  });

  it("never inserts above the header", () => {
    expect(insertRow(table(3), -1).rows[0]).toEqual(["Name", "Count", "Note"]);
  });

  it("removes the caret row but protects the header", () => {
    expect(removeRow(table(4))!.rows.map((row) => row[0])).toEqual(["Name", "b"]);
    expect(removeRow(table(3))).toBeNull();
  });

  it("moves rows within the body only", () => {
    expect(moveRow(table(5), -1)!.rows.map((row) => row[0])).toEqual(["Name", "b", "a"]);
    expect(moveRow(table(4), -1)).toBeNull();
    expect(moveRow(table(5), 1)).toBeNull();
  });

  it("inserts a column with its alignment", () => {
    const next = insertColumn(table(4, 8), 1);
    expect(next.rows[0]).toEqual(["Name", "Count", "", "Note"]);
    expect(next.align).toEqual(["left", "right", "none", "center"]);
    expect(next.column).toBe(2);
  });

  it("removes a column and keeps the last one", () => {
    const next = removeColumn(table(4, 8))!;
    expect(next.rows[0]).toEqual(["Name", "Note"]);
    expect(next.align).toEqual(["left", "center"]);
    const single = findTableAt(linesOf("| only |\n| --- |\n| x |"), 0)!;
    expect(removeColumn(single)).toBeNull();
  });

  it("moves a column with its cells and alignment", () => {
    const next = moveColumn(table(4, 8), -1)!;
    expect(next.rows[0]).toEqual(["Count", "Name", "Note"]);
    expect(next.rows[1]).toEqual(["1", "a", "yes"]);
    expect(next.align).toEqual(["right", "left", "center"]);
    expect(moveColumn(table(4), -1)).toBeNull();
  });

  it("changes the alignment of the caret column", () => {
    const next = setColumnAlign(table(4, 8), "center");
    expect(next.align).toEqual(["left", "center", "center"]);
    expect(formatTable(next).split("\n")[1]).toBe("| :--- | :---: | :--: |");
  });

  it("leaves the source table untouched", () => {
    const info = table(4, 8);
    insertRow(info, 1);
    insertColumn(info, 1);
    removeColumn(info);
    expect(info.rows).toEqual([
      ["Name", "Count", "Note"],
      ["a", "1", "yes"],
      ["b", "22", "no"],
    ]);
  });
});
