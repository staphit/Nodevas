/**
 * The right-click menu on empty space in the explorer [B-06].
 *
 * It acts on the workspace rather than on a row, so it collects the actions
 * that have no row of their own: reveal, remove, create and import.
 */

import { useEffect, useState } from "react";
import {
  IconClose,
  IconExport,
  IconFolder,
  IconFolderOpen,
  IconImport,
  IconPlus,
} from "../../icons";
import { reportError, useApp } from "../../store";
import type { WorkspaceEntry } from "../../state/types";
import { confirmAction } from "../ConfirmDialog";
import type { ProjectCreateTarget } from "./ProjectCreatePanel";
import type { ProjectTransfer } from "./useProjectTransfer";

export function TreeContextMenu({
  menu,
  onClose,
  workspace,
  workspaceName,
  workspaceRoots,
  activeProject,
  openFolderInExplorer,
  folderOpenBusy,
  beginProjectCreate,
  openWorkspacePicker,
  importPathBusy,
  transfer,
  setProjectTransferNotice,
}: {
  menu: { x: number; y: number };
  onClose: () => void;
  workspace: string;
  workspaceName: string;
  workspaceRoots: WorkspaceEntry[];
  activeProject: string;
  openFolderInExplorer: (path: string, label: string) => Promise<void>;
  folderOpenBusy: string | null;
  beginProjectCreate: (target: ProjectCreateTarget) => void;
  openWorkspacePicker: () => void;
  importPathBusy: boolean;
  transfer: ProjectTransfer;
  setProjectTransferNotice: (notice: string | null) => void;
}) {
  const removeWorkspace = useApp((state) => state.removeWorkspace);
  const [workspaceRemoveBusy, setWorkspaceRemoveBusy] = useState(false);
  const treeContextMenuTop = Math.max(
    8,
    Math.min(menu.y, window.innerHeight - 260),
  );

  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      const target = event.target as Element | null;
      if (!target?.closest(".tree-context-menu")) onClose();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("pointerdown", closeOutside);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [menu]);

  const removeCurrentWorkspace = async () => {
    if (!workspace || workspaceRemoveBusy) return;
    const fallback = workspaceRoots.find((root) => root.path !== workspace);
    if (!fallback) return;
    const confirmed = await confirmAction({
      title: `從清單移除「${workspaceName}」？`,
      description:
        `只會移除工作區紀錄；${workspace} 的資料夾與檔案完全保留。` +
        `完成後會切換到「${fallback.label}」。`,
      confirmLabel: "移除工作區",
      tone: "danger",
    });
    if (!confirmed) return;
    setWorkspaceRemoveBusy(true);
    onClose();
    try {
      await removeWorkspace(workspace);
      setProjectTransferNotice(`已移除工作區：${workspaceName}（磁碟檔案保留）`);
    } catch (error) {
      setProjectTransferNotice(`無法移除工作區：${(error as Error).message}`);
      reportError(error);
    } finally {
      setWorkspaceRemoveBusy(false);
    }
  };

  return (
    <div
      className="project-context-menu tree-context-menu"
      role="menu"
      aria-label="工作區操作"
      style={{
        left: Math.max(8, Math.min(menu.x, window.innerWidth - 282)),
        top: treeContextMenuTop,
        maxHeight: Math.max(120, window.innerHeight - treeContextMenuTop - 8),
        overflowY: "auto",
      }}
      onContextMenu={(event) => event.preventDefault()}
    >
      <div className="project-context-heading">
        <IconFolderOpen size={14} />
        <span>
          <b>{workspaceName}</b>
          <small>工作區</small>
        </span>
      </div>
      <button
        type="button"
        role="menuitem"
        disabled={!workspace || folderOpenBusy !== null}
        onClick={() => {
          onClose();
          void openFolderInExplorer(workspace, workspaceName);
        }}
      >
        <span className="project-context-icon">
          <IconFolderOpen size={14} />
        </span>
        <span>
          <b>在檔案總管中開啟</b>
          <small>{workspace || "工作區路徑不可用"}</small>
        </span>
      </button>
      {workspaceRoots.length <= 1 ? (
        <button type="button" role="menuitem" disabled>
          <span className="project-context-icon">
            <IconClose size={14} />
          </span>
          <span>
            <b>最後一個工作區不可移除</b>
            <small>請先加入另一個工作區</small>
          </span>
        </button>
      ) : (
        <button
          type="button"
          role="menuitem"
          className="danger"
          disabled={workspaceRemoveBusy}
          onClick={() => void removeCurrentWorkspace()}
        >
          <span className="project-context-icon">
            <IconClose size={14} />
          </span>
          <span>
            <b>{workspaceRemoveBusy ? "移除中…" : "移除工作區"}</b>
            <small>從清單移除，保留磁碟檔案</small>
          </span>
        </button>
      )}
      <div className="project-context-separator" />
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          beginProjectCreate({ mode: "project", parent: "" });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconPlus size={14} />
        </span>
        <span>
          <b>新專案</b>
          <small>在工作區最上層建立專案目錄</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={!activeProject}
        onClick={() => {
          beginProjectCreate({ mode: "project", parent: activeProject });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconFolder size={14} />
        </span>
        <span>
          <b>新增子專案</b>
          <small>建立在目前專案底下</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          beginProjectCreate({ mode: "folder", parent: "" });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconFolderOpen size={14} />
        </span>
        <span>
          <b>新增工作區目錄</b>
          <small>純資料夾，用來分組專案</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={importPathBusy}
        onClick={() => {
          onClose();
          openWorkspacePicker();
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>加入工作區</b>
          <small>保留原位置，加入最上層工作區清單</small>
        </span>
      </button>
      <div className="project-context-separator" />
      {/* Export sits with the imports because a person looking for one is
          looking for the other, and a menu that offers only the inbound half
          reads as though the outbound half lives somewhere else. A row's own
          menu exports that project (ProjectContextMenu); this menu acts on the
          workspace, so it exports the whole thing — the same bundle the
          scheduled backup pushes. */}
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          transfer.exportProjectArchive({ name: ".", label: workspaceName });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconExport size={14} />
        </span>
        <span>
          <b>匯出整個工作區</b>
          <small>.veproj 封裝，含所有專案與資料夾結構</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={transfer.markdownImportBusy}
        onClick={() => {
          transfer.markdownImportInputRef.current?.click();
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入 MD</b>
          <small>把 Markdown 檔加入目前專案</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={transfer.projectTransferBusy}
        onClick={() => {
          transfer.importInputRef.current?.click();
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入專案</b>
          <small>.veproj 或 .zip 專案封裝</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={transfer.jsonCanvasBusy}
        onClick={() => {
          transfer.jsonCanvasImportInputRef.current?.click();
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入 Canvas</b>
          <small>Obsidian JSON Canvas（.canvas）</small>
        </span>
      </button>
    </div>
  );
}
