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
import { useI18n } from "../../i18n";

const HEADING_LABELS = ["H1", "H2", "H3"] as const;

const MARKDOWN_HINTS = [
  ["# Heading", "heading"],
  ["**bold**", "bold"],
  ["*italic*", "italic"],
  ["~~strike~~", "strike"],
  ["`code`", "inlineCode"],
  ["```js … ```", "codeBlock"],
  ["- item / * item", "bullet"],
  ["1. item", "number"],
  ["- [ ] task", "task"],
  ["> quote", "quote"],
  ["[text](https://…)", "link"],
  ["![alt](image.png)", "image"],
  ["---", "divider"],
  ["| A | B |", "table"],
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
  const { t } = useI18n();
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
    <div className="editor-toolbar" role="toolbar" aria-label={t("editor.toolbar")}>
      {/* Markdown markers only make sense in a Markdown-edited page. */}
      {markdownTools && (
        <>
      <button
        type="button"
        className="tool-btn"
        onClick={() => setHeading(view(), 0)}
        title={t("editor.paragraph")}
        aria-label={t("editor.setParagraph")}
      >
        <IconParagraph size={14} />
      </button>
      {HEADING_LABELS.map((heading, index) => (
        <button
          key={heading}
          type="button"
          className="tool-btn mono"
          onClick={() => setHeading(view(), index + 1)}
          title={t("editor.heading", { level: String(index + 1) })}
        >
          {heading}
        </button>
      ))}
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "**", "**")}
        title={t("editor.bold")}
        aria-label={t("editor.bold")}
      >
        <b>B</b>
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "*", "*")}
        title={t("editor.italic")}
        aria-label={t("editor.italic")}
      >
        <i>I</i>
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "~~", "~~")}
        title={t("editor.strike")}
        aria-label={t("editor.strike")}
      >
        <s>S</s>
      </button>
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "dash")}
        title={t("editor.bulletList")}
        aria-label={t("editor.bulletList")}
      >
        <IconListBullet size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "number")}
        title={t("editor.orderedList")}
        aria-label={t("editor.orderedList")}
      >
        <IconListOrdered size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => applyListStyle(view(), "check")}
        title={t("editor.taskList")}
        aria-label={t("editor.taskList")}
      >
        <IconListTask size={14} />
      </button>
      <span className="tool-sep" />
      <button
        type="button"
        className="tool-btn"
        onClick={() => prefixLines(view(), "> ")}
        title={t("editor.quote")}
        aria-label={t("editor.quote")}
      >
        <IconQuote size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={() => wrapSelection(view(), "[", `](${t("editor.urlPlaceholder")})`, t("editor.linkTextPlaceholder"))}
        title={t("editor.insertLink")}
        aria-label={t("editor.insertLink")}
      >
        <IconLink size={14} />
      </button>
      <button
        type="button"
        className="tool-btn"
        onClick={onInsertNodeLink}
        title={t("editor.linkToNode")}
        aria-label={t("editor.linkToNodeLabel")}
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
        title={t("editor.insertAttachment")}
        aria-label={t("editor.insertAttachmentLabel")}
      >
        <IconImage size={14} />
      </button>
      {markdownTools && (
      <details className="editor-tool-more" ref={tableMenuRef}>
        <summary
          className={`tool-btn${tableAt ? " on" : ""}`}
          role="button"
          title={t("editor.insertTable")}
          aria-label={t("editor.insertTable")}
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
              {t("editor.rows")}
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
              {t("editor.columns")}
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
              {t("editor.insert")}
            </button>
          </form>
          <div className="editor-tool-more-hint">
            {t("editor.tableHint")}
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
          title={t("editor.moreFormatting")}
          aria-label={t("editor.moreFormatting")}
        >
          <IconMore size={14} />
        </summary>
        <div className="editor-tool-more-panel">
          <div className="editor-tool-more-row">
            <button
              type="button"
              className="tool-btn mono"
              onClick={() => wrapSelection(view(), "`", "`")}
              title={t("editor.inlineCode")}
            >
              {"<>"}
            </button>
            <select
              className="tool-list-select"
              value=""
              aria-label={t("editor.otherList")}
              onChange={(event) => {
                if (event.target.value) applyListStyle(view(), event.target.value as ListStyle);
              }}
            >
              <option value="">{t("editor.otherListOption")}</option>
              <option value="star">{t("editor.starItem")}</option>
              <option value="plus">{t("editor.plusItem")}</option>
              <option value="alpha-upper">A. B. C.</option>
              <option value="alpha-lower">a. b. c.</option>
              <option value="roman-upper">I. II. III.</option>
              <option value="roman-lower">i. ii. iii.</option>
            </select>
          </div>
          <div className="editor-tool-more-row">
            <span className="editor-tool-more-label">{t("editor.fontSize")}</span>
            <button
              type="button"
              className="tool-btn"
              onClick={() => setFontSize(fontSize - 1)}
              title={t("editor.decreaseFont")}
            >
              A-
            </button>
            <span className="tool-fs mono">{fontSize}</span>
            <button
              type="button"
              className="tool-btn"
              onClick={() => setFontSize(fontSize + 1)}
              title={t("editor.increaseFont")}
            >
              A+
            </button>
          </div>
          <div className="markdown-help-title">{t("editor.markdownHelp")}</div>
          <div className="markdown-help-grid">
            {MARKDOWN_HINTS.map(([syntax, meaning]) => (
              <div className="markdown-help-row" key={syntax}>
                <code>{syntax}</code>
                <span>{t(`editor.markdown.${meaning}`)}</span>
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
        title={t("editor.outlineTitle")}
        aria-label={t("editor.outline")}
      >
        <IconOutline size={14} />
      </button>
      <div className="editor-mode-switch" role="group" aria-label={t("editor.mode")}>
        {markdownTools && (
          <button
            type="button"
            className={`tool-btn${editorMode === "live" ? " on" : ""}`}
            aria-pressed={editorMode === "live"}
            onClick={() => setEditorMode("live")}
            title={t("editor.liveTitle")}
            aria-label={t("editor.liveLabel")}
          >
            <IconPencil size={13} />
          </button>
        )}
        <button
          type="button"
          className={`tool-btn mono${editorMode === "source" ? " on" : ""}`}
          aria-pressed={editorMode === "source"}
          onClick={() => setEditorMode("source")}
          title={t("editor.sourceTitle")}
          aria-label={t("editor.sourceLabel")}
        >
          {"<>"}
        </button>
        <button
          type="button"
          className={`tool-btn${editorMode === "preview" ? " on" : ""}`}
          aria-pressed={editorMode === "preview"}
          onClick={() => setEditorMode("preview")}
          title={t("editor.previewTitle")}
          aria-label={t("editor.previewLabel")}
        >
          <IconEye size={13} />
        </button>
      </div>
    </div>
  );
}
