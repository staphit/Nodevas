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
import { useI18n } from "../../i18n";
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
  const { t } = useI18n();
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
      title: t("explorer.removeWorkspaceTitle", { workspace: workspaceName }),
      description: t("explorer.removeWorkspaceDescription", {
        path: workspace,
        fallback: fallback.label,
      }),
      confirmLabel: t("explorer.removeWorkspace"),
      tone: "danger",
    });
    if (!confirmed) return;
    setWorkspaceRemoveBusy(true);
    onClose();
    try {
      await removeWorkspace(workspace);
      setProjectTransferNotice(t("explorer.workspaceRemoved", { workspace: workspaceName }));
    } catch (error) {
      setProjectTransferNotice(t("explorer.removeWorkspaceFailed", { error: (error as Error).message }));
      reportError(error);
    } finally {
      setWorkspaceRemoveBusy(false);
    }
  };

  return (
    <div
      className="project-context-menu tree-context-menu"
      role="menu"
      aria-label={t("explorer.workspaceActions")}
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
            <small>{t("explorer.workspace")}</small>
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
          <b>{t("explorer.openInExplorer")}</b>
          <small>{workspace || t("explorer.workspacePathUnavailable")}</small>
        </span>
      </button>
      {workspaceRoots.length <= 1 ? (
        <button type="button" role="menuitem" disabled>
          <span className="project-context-icon">
            <IconClose size={14} />
          </span>
          <span>
            <b>{t("explorer.lastWorkspaceUnavailable")}</b>
            <small>{t("explorer.addAnotherWorkspace")}</small>
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
            <b>{workspaceRemoveBusy ? t("explorer.removing") : t("explorer.removeWorkspace")}</b>
            <small>{t("explorer.keepFiles")}</small>
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
          <b>{t("explorer.newProject")}</b>
          <small>{t("explorer.newProjectHint")}</small>
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
          <b>{t("explorer.newChildProject")}</b>
          <small>{t("explorer.newChildProjectHint")}</small>
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
          <b>{t("explorer.newWorkspaceFolder")}</b>
          <small>{t("explorer.newWorkspaceFolderHint")}</small>
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
          <b>{t("explorer.addWorkspace")}</b>
          <small>{t("explorer.addWorkspaceHint")}</small>
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
          <b>{t("explorer.exportWorkspace")}</b>
          <small>{t("explorer.exportWorkspaceHint")}</small>
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
          <b>{t("explorer.importMarkdown")}</b>
          <small>{t("explorer.importMarkdownHint")}</small>
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
          <b>{t("explorer.importArchive")}</b>
          <small>{t("explorer.importArchiveHint")}</small>
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
          <b>{t("explorer.importCanvas")}</b>
          <small>{t("explorer.importCanvasHint")}</small>
        </span>
      </button>
    </div>
  );
}
