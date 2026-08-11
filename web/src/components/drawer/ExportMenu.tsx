import { useCallback, useRef, useState } from "react";

import type { ExportFormat, ExportScope } from "../../api";
import { downloadExport, printExport } from "../../editor/documentExport";
import { IconExport } from "../../icons";

const EXPORT_SCOPES: readonly (readonly [ExportScope, string, string])[] = [
  ["page", "這一頁", "目前開啟的頁面"],
  ["node", "整個節點", "主頁加上所有子頁"],
  ["project", "整個專案", "所有節點合成一份"],
];

const EXPORT_FORMATS: readonly (readonly [ExportFormat, string, string])[] = [
  ["docx", "Word", ".docx"],
  ["txt", "純文字", ".txt"],
  ["html", "網頁", ".html"],
  ["md", "Markdown", ".md"],
];

// The unsaved buffer travels with the request, so what downloads is what is
// on screen rather than what last reached the disk.
export function ExportMenu({
  nodeId,
  pageId,
  content,
}: {
  nodeId: string;
  pageId: string;
  content: string;
}) {
  const [exportScope, setExportScope] = useState<ExportScope>("page");
  const [exportBusy, setExportBusy] = useState(false);
  const [exportNotice, setExportNotice] = useState<string | null>(null);
  const exportMenuRef = useRef<HTMLDetailsElement>(null);

  const exportRequest = useCallback(
    (format: ExportFormat, scope: ExportScope) => ({
      format,
      scope,
      nodeId,
      pageId,
      content,
    }),
    [content, nodeId, pageId],
  );

  const runExport = useCallback(
    async (format: ExportFormat, scope: ExportScope) => {
      setExportBusy(true);
      setExportNotice(null);
      try {
        const name = await downloadExport(exportRequest(format, scope));
        setExportNotice(`已匯出 ${name}`);
        exportMenuRef.current?.removeAttribute("open");
      } catch (error) {
        setExportNotice(`匯出失敗：${(error as Error).message}`);
      } finally {
        setExportBusy(false);
      }
    },
    [exportRequest],
  );

  const runPrint = useCallback(
    async (scope: ExportScope) => {
      setExportBusy(true);
      setExportNotice(null);
      try {
        const { format: _format, ...request } = exportRequest("html", scope);
        await printExport(request);
        setExportNotice("已開啟列印視窗，在目的地選擇「另存為 PDF」");
        exportMenuRef.current?.removeAttribute("open");
      } catch (error) {
        setExportNotice(`PDF 匯出失敗：${(error as Error).message}`);
      } finally {
        setExportBusy(false);
      }
    },
    [exportRequest],
  );

  return (
    <details className="editor-tool-more editor-export" ref={exportMenuRef}>
      <summary
        className="tool-btn"
        role="button"
        title="匯出檔案（Word / 純文字 / 網頁 / PDF）"
        aria-label="匯出檔案"
      >
        <IconExport size={14} />
      </summary>
      <div className="editor-tool-more-panel editor-export-panel">
        <div className="editor-export-title">匯出範圍</div>
        <div className="editor-export-scopes" role="group" aria-label="匯出範圍">
          {EXPORT_SCOPES.map(([scope, label, hint]) => (
            <button
              key={scope}
              type="button"
              className={exportScope === scope ? "active" : ""}
              aria-pressed={exportScope === scope}
              title={hint}
              onClick={() => setExportScope(scope)}
            >
              {label}
            </button>
          ))}
        </div>
        <div className="editor-export-title">格式</div>
        <div className="editor-export-formats">
          {EXPORT_FORMATS.map(([format, label, extension]) => (
            <button
              key={format}
              type="button"
              disabled={exportBusy}
              onClick={() => void runExport(format, exportScope)}
            >
              <b>{label}</b>
              <span className="mono">{extension}</span>
            </button>
          ))}
          <button
            type="button"
            disabled={exportBusy}
            onClick={() => void runPrint(exportScope)}
          >
            <b>PDF</b>
            <span>列印對話框</span>
          </button>
        </div>
        {exportNotice && (
          <div className="editor-export-notice">{exportNotice}</div>
        )}
      </div>
    </details>
  );
}
