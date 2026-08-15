import { useCallback, useRef, useState } from "react";

import type { ExportFormat, ExportScope } from "../../api";
import { downloadExport, printExport } from "../../editor/documentExport";
import { IconExport } from "../../icons";
import { useI18n } from "../../i18n";

const EXPORT_SCOPES: readonly (readonly [ExportScope, string, string])[] = [
  ["page", "page", "pageHint"],
  ["node", "node", "nodeHint"],
  ["project", "project", "projectHint"],
];

const EXPORT_FORMATS: readonly (readonly [ExportFormat, string, string])[] = [
  ["docx", "docx", ".docx"],
  ["txt", "txt", ".txt"],
  ["html", "html", ".html"],
  ["md", "md", ".md"],
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
  const { t } = useI18n();
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
        setExportNotice(t("export.exported", { name }));
        exportMenuRef.current?.removeAttribute("open");
      } catch (error) {
        setExportNotice(t("export.failed", { error: (error as Error).message }));
      } finally {
        setExportBusy(false);
      }
    },
    [exportRequest, t],
  );

  const runPrint = useCallback(
    async (scope: ExportScope) => {
      setExportBusy(true);
      setExportNotice(null);
      try {
        const { format: _format, ...request } = exportRequest("html", scope);
        await printExport(request);
        setExportNotice(t("export.printOpened"));
        exportMenuRef.current?.removeAttribute("open");
      } catch (error) {
        setExportNotice(t("export.pdfFailed", { error: (error as Error).message }));
      } finally {
        setExportBusy(false);
      }
    },
    [exportRequest, t],
  );

  return (
    <details className="editor-tool-more editor-export" ref={exportMenuRef}>
      <summary
        className="tool-btn"
        role="button"
        title={t("export.fileTitle")}
        aria-label={t("export.fileLabel")}
      >
        <IconExport size={14} />
      </summary>
      <div className="editor-tool-more-panel editor-export-panel">
        <div className="editor-export-title">{t("export.scopeTitle")}</div>
        <div className="editor-export-scopes" role="group" aria-label={t("export.scopeTitle")}>
          {EXPORT_SCOPES.map(([scope, label, hint]) => (
            <button
              key={scope}
              type="button"
              className={exportScope === scope ? "active" : ""}
              aria-pressed={exportScope === scope}
              title={t(`export.scope.${hint}`)}
              onClick={() => setExportScope(scope)}
            >
              {t(`export.scope.${label}`)}
            </button>
          ))}
        </div>
        <div className="editor-export-title">{t("export.format")}</div>
        <div className="editor-export-formats">
          {EXPORT_FORMATS.map(([format, label, extension]) => (
            <button
              key={format}
              type="button"
              disabled={exportBusy}
              onClick={() => void runExport(format, exportScope)}
            >
              <b>{t(`format.${label}`)}</b>
              <span className="mono">{extension}</span>
            </button>
          ))}
          <button
            type="button"
            disabled={exportBusy}
            onClick={() => void runPrint(exportScope)}
          >
            <b>PDF</b>
            <span>{t("export.printDialog")}</span>
          </button>
        </div>
        {exportNotice && (
          <div className="editor-export-notice">{exportNotice}</div>
        )}
      </div>
    </details>
  );
}
