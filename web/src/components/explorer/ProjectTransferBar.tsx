/**
 * The project-file row of the explorer [B-06].
 *
 * Saving, save-as and the two transfer menus, plus the hidden file inputs every
 * import goes through. The inputs are rendered next to the row rather than
 * inside it so the workspace context menu can click them as well.
 */

import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { type ExportFormat } from "../../api";
import { useI18n } from "../../i18n";
import { clampToViewport, type PanelPoint } from "../floatingPanel";
import { reportError, useApp } from "../../store";
import { ImportBundleDialog } from "./ImportBundleDialog";
import type { ProjectTransfer } from "./useProjectTransfer";

/** Document formats the whole project can be exported as. */
const DOCUMENT_EXPORTS: readonly (readonly [
  ExportFormat | "pdf",
  string,
  string,
])[] = [
  ["docx", "explorer.documentExport.docx", "explorer.documentExport.docxHint"],
  ["txt", "explorer.documentExport.txt", "explorer.documentExport.txtHint"],
  ["html", "explorer.documentExport.html", "explorer.documentExport.htmlHint"],
  ["md", "explorer.documentExport.md", "explorer.documentExport.mdHint"],
  ["pdf", "explorer.documentExport.pdf", "explorer.documentExport.pdfHint"],
];

/**
 * Where a transfer panel goes, now that it is fixed rather than absolute.
 *
 * Anchored under the right edge of its own summary, then clamped so a panel
 * opened near the bottom or the right of the window still fits. Follows scroll
 * because the summary moves with the explorer's own scrolling.
 */
function useTransferMenuPlacement(menuRef: RefObject<HTMLDetailsElement | null>) {
  const panelRef = useRef<HTMLDivElement>(null);
  const [style, setStyle] = useState<PanelPoint | undefined>(undefined);

  useLayoutEffect(() => {
    const details = menuRef.current;
    if (!details) return;
    const place = () => {
      const summary = details.querySelector("summary");
      const panel = panelRef.current;
      if (!details.open || !summary || !panel) {
        setStyle(undefined);
        return;
      }
      const rect = summary.getBoundingClientRect();
      setStyle(
        clampToViewport(
          { left: rect.right - panel.offsetWidth, top: rect.bottom + 6 },
          panel.offsetWidth,
          panel.offsetHeight,
        ),
      );
    };
    place();
    details.addEventListener("toggle", place);
    window.addEventListener("resize", place);
    // Capture: the explorer scrolls, not the window.
    window.addEventListener("scroll", place, true);
    return () => {
      details.removeEventListener("toggle", place);
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [menuRef]);

  return { panelRef, style };
}

export function ProjectTransferBar({
  transfer,
  activeProject,
  setProjectTransferNotice,
  openSaveAs,
  saveAsBusy,
}: {
  transfer: ProjectTransfer;
  activeProject: string;
  setProjectTransferNotice: (notice: string | null) => void;
  openSaveAs: () => void;
  saveAsBusy: boolean;
}) {
  const {
    importInputRef,
    markdownImportInputRef,
    jsonCanvasImportInputRef,
    importMenuRef,
    exportMenuRef,
    projectTransferBusy,
    markdownImportBusy,
    jsonCanvasBusy,
    documentExportBusy,
    closeTransferMenus,
    exportProjectArchive,
    exportProjectDocument,
    importProject,
    pendingBundleImport,
    confirmBundleImport,
    cancelBundleImport,
    importTarget,
    importMarkdownFiles,
    exportJSONCanvas,
    importJSONCanvas,
  } = transfer;
  const importPlacement = useTransferMenuPlacement(importMenuRef);
  const exportPlacement = useTransferMenuPlacement(exportMenuRef);
  const { t } = useI18n();
  const saveAllTabs = useApp((state) => state.saveAllTabs);
  const dirtyDocumentCount = useApp(
    (state) =>
      state.tabs.filter((tab) => tab.dirty).length +
      Object.values(state.pageDocs).filter((doc) => doc.dirty && !doc.loading)
        .length,
  );
  const [projectSaveBusy, setProjectSaveBusy] = useState(false);

  // Everything else in the workspace autosaves; documents are the one manual
  // domain, so "save the project" means "flush every dirty document".
  const saveProject = async () => {
    if (projectSaveBusy || !activeProject) return;
    setProjectSaveBusy(true);
    try {
      const saved = await saveAllTabs();
      setProjectTransferNotice(
        saved > 0
          ? t("explorer.savedCount", { count: saved })
          : t("explorer.noUnsavedChanges"),
      );
    } catch (error) {
      setProjectTransferNotice(
        t("explorer.saveFailed", { error: (error as Error).message }),
      );
      reportError(error);
    } finally {
      setProjectSaveBusy(false);
    }
  };

  return (
    <>
      <input
        ref={markdownImportInputRef}
        className="visually-hidden"
        type="file"
        accept=".md,.markdown,text/markdown"
        multiple
        onChange={(event) => {
          void importMarkdownFiles(event.target.files);
        }}
      />
      {/* Two menus instead of a row of look-alike buttons: everything
          that comes in on one side, everything that goes out on the other. */}
      <div className="explorer-transfer-actions">
        <span>{t("explorer.projectFiles")}</span>
        <div>
          <button
            type="button"
            onClick={() => void saveProject()}
            disabled={projectSaveBusy || !activeProject}
            title={t("explorer.saveAllTitle")}
          >
            {projectSaveBusy
              ? t("explorer.saving")
              : dirtyDocumentCount > 0
                ? t("explorer.saveCount", { count: dirtyDocumentCount })
                : t("common.save")}
          </button>
          <button
            type="button"
            onClick={openSaveAs}
            disabled={saveAsBusy || !activeProject}
            title={t("explorer.saveAsTitle")}
          >
            {t("explorer.saveAs")}
          </button>
          <details className="transfer-menu" ref={importMenuRef}>
            <summary role="button" aria-label={t("explorer.importSource")} title={t("explorer.importTitle")}>
              {t("explorer.import")} ▾
            </summary>
            <div
              className="transfer-menu-panel"
              role="menu"
              ref={importPlacement.panelRef}
              style={importPlacement.style}
            >
              <button
                type="button"
                role="menuitem"
                disabled={projectTransferBusy}
                onClick={() => {
                  importInputRef.current?.click();
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.projectBundle")}</b>
                <small>{t("explorer.projectBundleHint")}</small>
              </button>
              <button
                type="button"
                role="menuitem"
                disabled={markdownImportBusy || !activeProject}
                onClick={() => {
                  markdownImportInputRef.current?.click();
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.markdownFiles")}</b>
                <small>{t("explorer.markdownFilesHint")}</small>
              </button>
              <button
                type="button"
                role="menuitem"
                disabled={jsonCanvasBusy || !activeProject}
                onClick={() => {
                  jsonCanvasImportInputRef.current?.click();
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.jsonCanvas")}</b>
                <small>{t("explorer.jsonCanvasHint")}</small>
              </button>
            </div>
          </details>
          <details className="transfer-menu" ref={exportMenuRef}>
            <summary role="button" aria-label={t("explorer.exportTarget")} title={t("explorer.exportTitle")}>
              {t("explorer.export")} ▾
            </summary>
            <div
              className="transfer-menu-panel"
              role="menu"
              ref={exportPlacement.panelRef}
              style={exportPlacement.style}
            >
              <button
                type="button"
                role="menuitem"
                disabled={projectTransferBusy || !activeProject}
                onClick={() => {
                  exportProjectArchive();
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.currentProjectBundle")}</b>
                <small>{t("explorer.currentProjectBundleHint")}</small>
              </button>
              <button
                type="button"
                role="menuitem"
                disabled={projectTransferBusy}
                onClick={() => {
                  exportProjectArchive({ name: ".", label: t("explorer.entireWorkspace") });
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.entireWorkspace")}</b>
                <small>{t("explorer.entireWorkspaceHint")}</small>
              </button>
              <button
                type="button"
                role="menuitem"
                disabled={jsonCanvasBusy || !activeProject}
                onClick={() => {
                  void exportJSONCanvas();
                  closeTransferMenus();
                }}
              >
                <b>{t("explorer.jsonCanvas")}</b>
                <small>{t("explorer.jsonCanvasExportHint")}</small>
              </button>
              <div className="transfer-menu-separator" />
              <div className="transfer-menu-label">{t("explorer.projectDocuments")}</div>
              {DOCUMENT_EXPORTS.map(([choice, label, hint]) => (
                <button
                  key={choice}
                  type="button"
                  role="menuitem"
                  disabled={documentExportBusy || !activeProject}
                  onClick={() => {
                    void exportProjectDocument(choice);
                    closeTransferMenus();
                  }}
                >
                  <b>{t(label)}</b>
                  <small>{t(hint)}</small>
                </button>
              ))}
            </div>
          </details>
        </div>
      </div>
      <input
        ref={importInputRef}
        className="visually-hidden"
        type="file"
        accept=".veproj,.zip,application/zip,application/vnd.nodevas.project+zip,application/vnd.visual-editor.project+zip"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void importProject(file);
        }}
      />
      {/* Next to the inputs rather than inside the row for the same reason
          they are: the workspace context menu opens the same import. */}
      {pendingBundleImport && (
        <ImportBundleDialog
          manifest={pendingBundleImport.manifest}
          busy={projectTransferBusy}
          target={importTarget}
          onCancel={cancelBundleImport}
          onConfirm={(choice) => void confirmBundleImport(choice)}
        />
      )}
      <input
        ref={jsonCanvasImportInputRef}
        className="visually-hidden"
        type="file"
        accept=".canvas,application/json"
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) void importJSONCanvas(file);
        }}
      />
    </>
  );
}
