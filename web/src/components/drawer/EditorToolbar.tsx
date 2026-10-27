import { useId, useState } from "react";
import type { EditorView } from "@codemirror/view";

import {
  IconChevronDown,
  IconExport,
  IconImage,
  IconLink,
  IconListBullet,
  IconListOrdered,
  IconListTask,
  IconOutline,
  IconParagraph,
  IconPencil,
  IconQuote,
} from "../../icons";
import { DrawerMenu, type DrawerMenuItem } from "./DrawerMenu";
import type { EditorMode } from "./editorExtensions";
import { ExportDialog } from "./ExportMenu";
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

/**
 * The format toolbar above the editor. Four markers are always in reach —
 * bold, italic, a bullet list and a link, the ones a note is mostly made of —
 * and "More formatting" holds everything else: headings, the other lists,
 * quotes and code, links to nodes, attachments, tables, export, the outline,
 * the editing mode and the text size.
 */
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
  pageTitle,
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
  pageTitle?: string;
  content: string;
}) {
  const { t } = useI18n();
  const fieldID = useId();
  const [exportOpen, setExportOpen] = useState(false);
  const { attachBusy, attachInputRef, attachFiles } = attachments;
  const { tableRows, setTableRows, tableColumns, setTableColumns, insertTable } = tables;
  const preview = editorMode === "preview";

  const markdownItems: DrawerMenuItem[] = markdownTools
    ? [
        {
          id: "paragraph",
          group: t("drawer.format.text"),
          label: t("editor.paragraph"),
          title: t("editor.setParagraph"),
          icon: <IconParagraph size={14} />,
          disabled: preview,
          onSelect: () => setHeading(view(), 0),
        },
        ...[1, 2, 3].map((level) => ({
          id: `heading-${level}`,
          label: t("editor.heading", { level: String(level) }),
          icon: <span className="mono">H{level}</span>,
          disabled: preview,
          onSelect: () => setHeading(view(), level),
        })),
        {
          id: "strike",
          label: t("editor.strike"),
          icon: <s>S</s>,
          disabled: preview,
          onSelect: () => wrapSelection(view(), "~~", "~~"),
        },
        {
          id: "code",
          label: t("editor.inlineCode"),
          icon: <span className="mono">{"<>"}</span>,
          disabled: preview,
          onSelect: () => wrapSelection(view(), "`", "`"),
        },
        {
          id: "ordered",
          separatorBefore: true,
          group: t("drawer.format.blocks"),
          label: t("editor.orderedList"),
          icon: <IconListOrdered size={14} />,
          disabled: preview,
          onSelect: () => applyListStyle(view(), "number"),
        },
        {
          id: "tasks",
          label: t("editor.taskList"),
          icon: <IconListTask size={14} />,
          disabled: preview,
          onSelect: () => applyListStyle(view(), "check"),
        },
        {
          id: "quote",
          label: t("editor.quote"),
          icon: <IconQuote size={14} />,
          disabled: preview,
          onSelect: () => prefixLines(view(), "> "),
        },
        {
          id: "node-link",
          separatorBefore: true,
          group: t("drawer.format.insert"),
          label: t("editor.linkToNodeLabel"),
          title: t("editor.linkToNode"),
          icon: <span className="mono">[[ ]]</span>,
          disabled: preview,
          onSelect: onInsertNodeLink,
        },
      ]
    : [];

  const items: DrawerMenuItem[] = [
    ...markdownItems,
    {
      id: "attachment",
      // Follows "link to node" in the Insert group, or starts it.
      group: markdownTools ? undefined : t("drawer.format.insert"),
      label: t("editor.insertAttachmentLabel"),
      title: t("editor.insertAttachment"),
      icon: <IconImage size={14} />,
      disabled: attachBusy,
      onSelect: () => attachInputRef.current?.click(),
    },
    {
      id: "export",
      separatorBefore: true,
      group: t("drawer.format.document"),
      label: t("drawer.format.export"),
      title: t("export.fileTitle"),
      icon: <IconExport size={14} />,
      onSelect: () => setExportOpen(true),
    },
    {
      id: "outline",
      label: t("editor.outline"),
      title: t("editor.outlineTitle"),
      icon: <IconOutline size={14} />,
      checked: outlineOpen,
      toggle: true,
      disabled: !markdownTools,
      onSelect: toggleOutline,
    },
    ...(markdownTools
      ? [
          {
            id: "mode-live",
            separatorBefore: true,
            group: t("editor.mode"),
            label: t("editor.liveLabel"),
            title: t("editor.liveTitle"),
            checked: editorMode === "live",
            onSelect: () => setEditorMode("live"),
          },
        ]
      : []),
    {
      id: "mode-source",
      separatorBefore: !markdownTools,
      group: markdownTools ? undefined : t("editor.mode"),
      label: t("editor.sourceLabel"),
      title: t("editor.sourceTitle"),
      checked: editorMode === "source",
      onSelect: () => setEditorMode("source"),
    },
    {
      id: "mode-preview",
      label: t("editor.previewLabel"),
      title: t("editor.previewTitle"),
      checked: editorMode === "preview",
      onSelect: () => setEditorMode("preview"),
    },
  ];

  return (
    <div className="editor-toolbar" role="toolbar" aria-label={t("editor.toolbar")}>
      {/* Markdown markers only make sense in a Markdown-edited page, and only
          while there is an editor to put them in. */}
      {markdownTools && (
        <>
          <button
            type="button"
            className="tool-btn"
            disabled={preview}
            onClick={() => wrapSelection(view(), "**", "**")}
            title={t("editor.bold")}
            aria-label={t("editor.bold")}
          >
            <b>B</b>
          </button>
          <button
            type="button"
            className="tool-btn"
            disabled={preview}
            onClick={() => wrapSelection(view(), "*", "*")}
            title={t("editor.italic")}
            aria-label={t("editor.italic")}
          >
            <i>I</i>
          </button>
          <button
            type="button"
            className="tool-btn"
            disabled={preview}
            onClick={() => applyListStyle(view(), "dash")}
            title={t("editor.bulletList")}
            aria-label={t("editor.bulletList")}
          >
            <IconListBullet size={14} />
          </button>
          <button
            type="button"
            className="tool-btn"
            disabled={preview}
            onClick={() =>
              wrapSelection(
                view(),
                "[",
                `](${t("editor.urlPlaceholder")})`,
                t("editor.linkTextPlaceholder"),
              )
            }
            title={t("editor.insertLink")}
            aria-label={t("editor.insertLink")}
          >
            <IconLink size={14} />
          </button>
        </>
      )}
      <DrawerMenu
        label={t("editor.moreFormatting")}
        trigger={
          <>
            <span>{t("editor.moreFormatting")}</span>
            <IconChevronDown size={12} />
          </>
        }
        triggerClassName="tool-btn editor-more-trigger"
        className="editor-more"
        items={items}
      >
        {(close) => (
          <>
            {markdownTools && !preview && (
              <form
                className="editor-tool-more-row"
                aria-label={t("editor.insertTable")}
                onSubmit={(event) => {
                  event.preventDefault();
                  insertTable(tableRows, tableColumns);
                  close();
                }}
              >
                <span className="editor-tool-more-label">{t("editor.insertTable")}</span>
                <input
                  id={`${fieldID}-table-rows`}
                  className="tool-number"
                  type="number"
                  min={1}
                  max={100}
                  value={tableRows}
                  aria-label={t("editor.rows")}
                  title={t("editor.rows")}
                  onChange={(event) => setTableRows(Number(event.target.value) || 1)}
                />
                <span aria-hidden="true">×</span>
                <input
                  id={`${fieldID}-table-columns`}
                  className="tool-number"
                  type="number"
                  min={1}
                  max={20}
                  value={tableColumns}
                  aria-label={t("editor.columns")}
                  title={t("editor.columns")}
                  onChange={(event) => setTableColumns(Number(event.target.value) || 1)}
                />
                <button type="submit" className="tool-btn">
                  {t("editor.insert")}
                </button>
              </form>
            )}
            {markdownTools && !preview && (
              <div className="editor-tool-more-row">
                <select
                  className="tool-list-select"
                  value=""
                  aria-label={t("editor.otherList")}
                  onChange={(event) => {
                    if (!event.target.value) return;
                    applyListStyle(view(), event.target.value as ListStyle);
                    close();
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
            )}
            <div className="editor-tool-more-row">
              <span className="editor-tool-more-label">{t("editor.fontSize")}</span>
              <button
                type="button"
                className="tool-btn"
                onClick={() => setFontSize(fontSize - 1)}
                title={t("editor.decreaseFont")}
                aria-label={t("editor.decreaseFont")}
              >
                A-
              </button>
              <span className="tool-fs mono">{fontSize}</span>
              <button
                type="button"
                className="tool-btn"
                onClick={() => setFontSize(fontSize + 1)}
                title={t("editor.increaseFont")}
                aria-label={t("editor.increaseFont")}
              >
                A+
              </button>
            </div>
            {markdownTools && (
              <details className="editor-markdown-help">
                <summary>{t("editor.markdownHelp")}</summary>
                <div className="markdown-help-grid">
                  {MARKDOWN_HINTS.map(([syntax, meaning]) => (
                    <div className="markdown-help-row" key={syntax}>
                      <code>{syntax}</code>
                      <span>{t(`editor.markdown.${meaning}`)}</span>
                    </div>
                  ))}
                </div>
              </details>
            )}
          </>
        )}
      </DrawerMenu>
      <input
        ref={attachInputRef}
        type="file"
        multiple
        hidden
        onChange={(event) => void attachFiles(event.target.files)}
      />
      <span className="tool-flex" />
      {/* Preview has no editor for the markers; the way back stays in view
          instead of behind the menu that led here. */}
      {preview && (
        <button
          type="button"
          className="tool-btn editor-back-to-edit"
          onClick={() => setEditorMode(markdownTools ? "live" : "source")}
          title={t("drawer.format.backToEditTitle")}
        >
          {markdownTools ? <IconPencil size={13} /> : <span className="mono">{"<>"}</span>}
          <span>{t("drawer.format.backToEdit")}</span>
        </button>
      )}
      {exportOpen && (
        <ExportDialog
          nodeId={nodeId}
          pageId={pageId}
          pageTitle={pageTitle}
          content={content}
          onClose={() => setExportOpen(false)}
        />
      )}
    </div>
  );
}
