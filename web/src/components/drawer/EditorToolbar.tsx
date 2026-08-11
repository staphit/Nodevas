import { useId } from "react";
import type { EditorView } from "@codemirror/view";

import {
  IconEye,
  IconImage,
  IconLink,
  IconListBullet,
  IconListOrdered,
  IconListTask,
  IconMore,
  IconOutline,
  IconParagraph,
  IconPencil,
  IconQuote,
  IconTable,
} from "../../icons";
import type { EditorMode } from "./editorExtensions";
import { ExportMenu } from "./ExportMenu";
import {
  applyListStyle,
  prefixLines,
  setHeading,
  wrapSelection,
  type ListStyle,
} from "./markdownCommands";
import type { Attachments } from "./useAttachments";
import type { TableEditing } from "./useTableEditing";

const HEADING_LABELS = ["H1", "H2", "H3"] as const;

const MARKDOWN_HINTS = [
  ["# 標題", "標題（支援 #～######）"],
  ["**粗體**", "粗體"],
  ["*斜體*", "斜體"],
  ["~~刪除線~~", "刪除線"],
  ["`code`", "行內程式碼"],
  ["```js … ```", "程式碼區塊"],
  ["- 項目 / * 項目", "無序清單"],
  ["1. 項目", "編號清單"],
  ["- [ ] 待辦", "Checklist"],
  ["> 引用", "引用區塊"],
  ["[文字](https://…)", "連結"],
  ["![說明](image.png)", "圖片"],
  ["---", "水平分隔線"],
  ["| A | B |", "表格"],
] as const;

/** The format toolbar above the editor: markers, attachments, export, mode. */
export function EditorToolbar({
  markdownTools,
  view,
  onInsertNodeLink,
  attachments,
  tables,
  fontSize,
  setFontSize,
  outlineOpen,
  toggleOutline,
  editorMode,
  setEditorMode,
  nodeId,
  pageId,
  content,
}: {
  markdownTools: boolean;
  view: () => EditorView | undefined;
  onInsertNodeLink: () => void;
  attachments: Attachments;
  tables: TableEditing;
  fontSize: number;
  setFontSize: (next: number) => void;
  outlineOpen: boolean;
  toggleOutline: () => void;
  editorMode: EditorMode;
  setEditorMode: (mode: EditorMode) => void;
  nodeId: string;
  pageId: string;
  content: string;
}) {
  const fieldID = useId();
  const { attachBusy, attachInputRef, attachFiles } = attachments;
  const {
    tableAt,
    tableRows,
    setTableRows,
    tableColumns,
    setTableColumns,
    tableMenuRef,
    insertTable,
  } = tables;

  return (
    <div className="editor-toolbar" role="toolbar" aria-label="格式">
      {/* Markdown markers only make sense in a Markdown-edited page. */}
      {markdownTools && (
        <>
      <button
        type="button"
        className="tool-btn"
        onClick={() => setHeading(view(), 0)}
        title="內文"
        aria-label="設為內文"
      >
        <IconParagraph size={14} />
      </button>
      {HEADING_LABELS.map((heading, index) => (
        <button
          key={heading}
          type="button"
          className="tool-btn mono"
          onClick={() => setHeading(view(), index + 1)}
          title={`標題 ${index + 1}`}
        >
          {heading}
        </button>
      ))}
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "**", "**")}
        title="粗體"
        aria-label="粗體"
      >
        <b>B</b>
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "*", "*")}
        title="斜體"
        aria-label="斜體"
      >
        <i>I</i>
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "~~", "~~")}
        title="刪除線"
        aria-label="刪除線"
      >
        <s>S</s>
      </button>
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "dash")}
        title="項目清單"
        aria-label="項目清單"
      >
        <IconListBullet size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "number")}
        title="編號清單"
        aria-label="編號清單"
      >
        <IconListOrdered size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "check")}
        title="待辦清單"
        aria-label="待辦清單"
      >
        <IconListTask size={14} />
      </button>
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => prefixLines(view(), "> ")}
        title="引用"
        aria-label="引用"
      >
        <IconQuote size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "[", "](網址)", "連結文字")}
        title="插入超連結"
        aria-label="插入超連結"
      >
        <IconLink size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={onInsertNodeLink}
        title="連結到節點（工作區內）"
        aria-label="連結到節點"
      >
        [[ ]]
      </button>
        </>
      )}
      <button
        type="button"
        className="tool-btn"
        disabled={attachBusy}
        onClick={() => attachInputRef.current?.click()}
        title="插入圖片或附件（上傳到此節點）"
        aria-label="插入圖片或附件"
      >
        <IconImage size={14} />
      </button>
      {markdownTools && (
      <details className="editor-tool-more" ref={tableMenuRef}>
        <summary
          className={`tool-btn${tableAt ? " on" : ""}`}
          role="button"
          title="插入表格"
          aria-label="插入表格"
        >
          <IconTable size={14} />
        </summary>
        <div className="editor-tool-more-panel">
          <form
            className="editor-tool-more-row"
            onSubmit={(event) => {
              event.preventDefault();
              insertTable(tableRows, tableColumns);
            }}
          >
            <label
              className="editor-tool-more-label"
              htmlFor={`${fieldID}-table-rows`}
            >
              列數
            </label>
            <input
              id={`${fieldID}-table-rows`}
              className="tool-number"
              type="number"
              min={1}
              max={100}
              value={tableRows}
              onChange={(event) =>
                setTableRows(Number(event.target.value) || 1)
              }
            />
            <label
              className="editor-tool-more-label"
              htmlFor={`${fieldID}-table-columns`}
            >
              欄數
            </label>
            <input
              id={`${fieldID}-table-columns`}
              className="tool-number"
              type="number"
              min={1}
              max={20}
              value={tableColumns}
              onChange={(event) =>
                setTableColumns(Number(event.target.value) || 1)
              }
            />
            <button type="submit" className="tool-btn">
              插入
            </button>
          </form>
          <div className="editor-tool-more-hint">
            列數不含標題列。游標移進表格後，工具列下方會出現欄列調整。
          </div>
        </div>
      </details>
      )}
      <input
        ref={attachInputRef}
        type="file"
        multiple
        hidden
        onChange={(event) => void attachFiles(event.target.files)}
      />

      <details className="editor-tool-more">
        <summary
          className="tool-btn"
          role="button"
          title="更多格式"
          aria-label="更多格式"
        >
          <IconMore size={14} />
        </summary>
        <div className="editor-tool-more-panel">
          <div className="editor-tool-more-row">
            <button
              type="button"
              className="tool-btn mono"
              onClick={() => wrapSelection(view(), "`", "`")}
              title="行內程式碼"
            >
              {"<>"}
            </button>
            <select
              className="tool-list-select"
              value=""
              aria-label="其他編號格式"
              onChange={(event) => {
                if (event.target.value) applyListStyle(view(), event.target.value as ListStyle);
              }}
            >
              <option value="">其他編號</option>
              <option value="star">* 項目</option>
              <option value="plus">+ 項目</option>
              <option value="alpha-upper">A. B. C.</option>
              <option value="alpha-lower">a. b. c.</option>
              <option value="roman-upper">I. II. III.</option>
              <option value="roman-lower">i. ii. iii.</option>
            </select>
          </div>
          <div className="editor-tool-more-row">
            <span className="editor-tool-more-label">字級</span>
            <button
              type="button"
              className="tool-btn"
              onClick={() => setFontSize(fontSize - 1)}
              title="縮小字體"
            >
              A-
            </button>
            <span className="tool-fs mono">{fontSize}</span>
            <button
              type="button"
              className="tool-btn"
              onClick={() => setFontSize(fontSize + 1)}
              title="放大字體"
            >
              A+
            </button>
          </div>
          <div className="markdown-help-title">Markdown 語法提示</div>
          <div className="markdown-help-grid">
            {MARKDOWN_HINTS.map(([syntax, meaning]) => (
              <div className="markdown-help-row" key={syntax}>
                <code>{syntax}</code>
                <span>{meaning}</span>
              </div>
            ))}
          </div>
        </div>
      </details>

      <span className="tool-flex" />
      <ExportMenu nodeId={nodeId} pageId={pageId} content={content} />
      <button
        type="button"
        className={`tool-btn${outlineOpen ? " on" : ""}`}
        aria-pressed={outlineOpen}
        disabled={!markdownTools}
        onClick={toggleOutline}
        title="文件目錄（標題大綱）"
        aria-label="文件目錄"
      >
        <IconOutline size={14} />
      </button>
      <div className="editor-mode-switch" role="group" aria-label="編輯模式">
        {markdownTools && (
          <button
            type="button"
            className={`tool-btn${editorMode === "live" ? " on" : ""}`}
            aria-pressed={editorMode === "live"}
            onClick={() => setEditorMode("live")}
            title="即時：直接以排版後的樣子編輯"
            aria-label="即時編輯"
          >
            <IconPencil size={13} />
          </button>
        )}
        <button
          type="button"
          className={`tool-btn mono${editorMode === "source" ? " on" : ""}`}
          aria-pressed={editorMode === "source"}
          onClick={() => setEditorMode("source")}
          title="原始碼：顯示完整 Markdown 標記"
          aria-label="原始碼"
        >
          {"<>"}
        </button>
        <button
          type="button"
          className={`tool-btn${editorMode === "preview" ? " on" : ""}`}
          aria-pressed={editorMode === "preview"}
          onClick={() => setEditorMode("preview")}
          title="預覽：唯讀排版"
          aria-label="預覽"
        >
          <IconEye size={13} />
        </button>
      </div>
    </div>
  );
}
