/**
 * The project-file row of the explorer [B-06].
 *
 * Saving, save-as and the two transfer menus, plus the hidden file inputs every
 * import goes through. The inputs are rendered next to the row rather than
 * inside it so the workspace context menu can click them as well.
 */

import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { type ExportFormat } from "../../api";
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
  ["docx", "Word", ".docx，可在 Word 直接編輯"],
  ["txt", "純文字", ".txt，剝掉所有標記"],
  ["html", "網頁", ".html，單檔可直接開"],
  ["md", "Markdown", ".md，所有節點合成一份"],
  ["pdf", "PDF", "開列印視窗，選「另存為 PDF」"],
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
        saved > 0 ? `已儲存 ${saved} 個文件` : "沒有未儲存的變更",
      );
    } catch (error) {
      setProjectTransferNotice(`儲存失敗：${(error as Error).message}`);
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
        <span>專案檔案</span>
        <div>
          <button
            type="button"
            onClick={() => void saveProject()}
            disabled={projectSaveBusy || !activeProject}
            title="把所有未儲存的文件寫入磁碟"
          >
            {projectSaveBusy
              ? "儲存中…"
              : dirtyDocumentCount > 0
                ? `儲存 ${dirtyDocumentCount}`
                : "儲存"}
          </button>
          <button
            type="button"
            onClick={openSaveAs}
            disabled={saveAsBusy || !activeProject}
            title="以新名稱複製一份專案並開啟"
          >
            另存新檔
          </button>
          <details className="transfer-menu" ref={importMenuRef}>
            <summary role="button" aria-label="匯入來源" title="把檔案帶進工作區">
              匯入 ▾
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
                <b>專案封裝</b>
                <small>.veproj 或 .zip，整個專案帶進來</small>
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
                <b>Markdown 檔案</b>
                <small>每個 .md 變成一個節點</small>
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
                <b>JSON Canvas</b>
                <small>Obsidian 等工具的 .canvas 圖面</small>
              </button>
            </div>
          </details>
          <details className="transfer-menu" ref={exportMenuRef}>
            <summary role="button" aria-label="匯出目標" title="把工作區的內容帶出去">
              匯出 ▾
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
                <b>目前專案封裝</b>
                <small>.veproj，含節點、子頁與執行紀錄</small>
              </button>
              <button
                type="button"
                role="menuitem"
                disabled={projectTransferBusy}
                onClick={() => {
                  exportProjectArchive({ name: ".", label: "整個工作區" });
                  closeTransferMenus();
                }}
              >
                <b>整個工作區</b>
                <small>.veproj 封裝，含所有子專案與資料夾結構</small>
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
                <b>JSON Canvas</b>
                <small>.canvas，通用圖面格式</small>
              </button>
              <div className="transfer-menu-separator" />
              <div className="transfer-menu-label">整個專案的文件</div>
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
                  <b>{label}</b>
                  <small>{hint}</small>
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
