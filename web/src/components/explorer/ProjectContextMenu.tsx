/**
 * The right-click menu on a project or workspace directory row [B-06].
 *
 * The destructive half of the menu lives here too: removing projects is only
 * ever started from this menu, and it needs the same target list the menu was
 * opened with.
 */

import { useEffect, useState } from "react";
import {
  IconClose,
  IconExport,
  IconFolder,
  IconFolderOpen,
  IconImport,
  IconPlus,
  IconTrash,
} from "../../icons";
import { api } from "../../api";
import { reportError, useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import type { ProjectCreateTarget } from "./ProjectCreatePanel";
import type { ProjectTransfer } from "./useProjectTransfer";

export type ProjectMenuTarget = {
  name: string;
  label: string;
  /** Every project the menu acts on: one, or the whole Ctrl-selection. */
  names: string[];
  path: string;
  isFolder: boolean;
  childCount: number;
  x: number;
  y: number;
};

export function ProjectContextMenu({
  menu,
  onClose,
  activeProject,
  expandedProjects,
  persistExpandedProjects,
  clearProjectSelection,
  setProjectTransferNotice,
  openFolderInExplorer,
  folderOpenBusy,
  exportProjectArchive,
  projectTransferBusy,
  projectRenameBusy,
  beginProjectCreate,
  transfer,
  onRename,
  onMove,
}: {
  menu: ProjectMenuTarget;
  onClose: () => void;
  activeProject: string;
  expandedProjects: Set<string>;
  persistExpandedProjects: (next: Set<string>) => void;
  clearProjectSelection: () => void;
  setProjectTransferNotice: (notice: string | null) => void;
  openFolderInExplorer: (path: string, label: string) => Promise<void>;
  folderOpenBusy: string | null;
  exportProjectArchive: (target?: { name: string; label: string }) => void;
  projectTransferBusy: boolean;
  projectRenameBusy: boolean;
  beginProjectCreate: (target: ProjectCreateTarget) => void;
  transfer: ProjectTransfer;
  onRename: (target: ProjectMenuTarget) => void;
  onMove: (target: ProjectMenuTarget) => void;
}) {
  const switchProject = useApp((state) => state.switchProject);
  const refreshProjects = useApp((state) => state.refreshProjects);
  const [projectRemoveBusy, setProjectRemoveBusy] = useState(false);
  const menuTop = Math.max(8, Math.min(menu.y, window.innerHeight - 250));

  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      const target = event.target as Element | null;
      if (!target?.closest(".project-context-menu")) onClose();
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    const closeMenu = () => onClose();
    window.addEventListener("pointerdown", closeOutside);
    window.addEventListener("keydown", closeOnEscape);
    window.addEventListener("resize", closeMenu);
    return () => {
      window.removeEventListener("pointerdown", closeOutside);
      window.removeEventListener("keydown", closeOnEscape);
      window.removeEventListener("resize", closeMenu);
    };
  }, [menu]);

  const removeProject = async (mode: "detach" | "disk") => {
    if (projectRemoveBusy) return;
    // Nested projects come off with their parent, so removing both would fail
    // on the second one.
    const targets = menu.names.filter(
      (name) =>
        !menu.names.some(
          (other) => other !== name && name.startsWith(`${other}/`),
        ),
    );
    const many = targets.length > 1;
    const hasChildren = menu.childCount > 0;
    const subject = many ? `${targets.length} 個專案` : `「${menu.label}」`;
    const confirmed = await confirmAction(
      mode === "disk"
        ? {
            title: many ? `永久刪除 ${targets.length} 個專案？` : `永久刪除${subject}？`,
            description: many
              ? `將刪除這些專案的磁碟資料夾：\n${targets.join("、")}\n此操作無法復原。`
              : `將刪除磁碟資料夾：${menu.path}${
                  hasChildren ? `，以及其中 ${menu.childCount} 個子專案` : ""
                }。此操作無法復原。`,
            confirmLabel: "永久刪除",
            tone: "danger",
          }
        : {
            title: many ? `解除匯入 ${targets.length} 個專案？` : `解除匯入${subject}？`,
            description: many
              ? `這些專案會從工作區清單移除，磁碟檔案保持不變：\n${targets.join("、")}`
              : `專案會從此工作區清單移除${
                  hasChildren ? `，其下 ${menu.childCount} 個子專案也會一併隱藏` : ""
                }；磁碟檔案保持不變。`,
            confirmLabel: "解除匯入",
          },
    );
    if (!confirmed) return;

    setProjectRemoveBusy(true);
    onClose();
    const removed: string[] = [];
    const failed: string[] = [];
    let active = "";
    try {
      // One request each: projects are separate directories, so a failure part
      // way through still leaves every earlier removal valid — the notice says
      // exactly what went and what did not.
      for (const name of targets) {
        try {
          const result = await api.removeProject(name, mode);
          removed.push(name);
          if (result.active) active = result.active;
        } catch (error) {
          failed.push(`${name}（${(error as Error).message}）`);
        }
      }
      const nextExpanded = new Set(
        [...expandedProjects].filter(
          (name) =>
            !removed.some(
              (gone) => name === gone || name.startsWith(`${gone}/`),
            ),
        ),
      );
      persistExpandedProjects(nextExpanded);
      clearProjectSelection();
      if (active && active !== activeProject) {
        await switchProject(active);
      } else {
        await refreshProjects();
      }
      const what = mode === "disk" ? "永久刪除" : "解除匯入";
      if (failed.length === 0) {
        setProjectTransferNotice(
          many
            ? `已${what} ${removed.length} 個專案`
            : `已${what}：${menu.label}${mode === "disk" ? "" : "（磁碟檔案保留）"}`,
        );
      } else {
        setProjectTransferNotice(
          `已${what} ${removed.length} 個，${failed.length} 個失敗：${failed.join("；")}`,
        );
      }
    } catch (error) {
      setProjectTransferNotice(`無法移除專案：${(error as Error).message}`);
      reportError(error);
    } finally {
      setProjectRemoveBusy(false);
    }
  };

  return (
    <div
      className="project-context-menu"
      role="menu"
      aria-label={
        menu.names.length > 1
          ? `${menu.names.length} 個專案操作`
          : `${menu.label} 專案操作`
      }
      style={{
        left: Math.max(8, Math.min(menu.x, window.innerWidth - 282)),
        top: menuTop,
        // With the create and import items the menu is taller than a short
        // window, so it scrolls inside itself rather than running off the
        // bottom — the same treatment TreeContextMenu gives its longer list.
        maxHeight: Math.max(120, window.innerHeight - menuTop - 8),
        overflowY: "auto",
      }}
      onContextMenu={(event) => event.preventDefault()}
    >
      <div className="project-context-heading">
        <IconFolder size={14} />
        <span>
          <b>{menu.label}</b>
          <small>{menu.path}</small>
        </span>
      </div>
      <button
        type="button"
        role="menuitem"
        disabled={!menu.path || folderOpenBusy !== null}
        onClick={() => {
          const target = menu;
          onClose();
          void openFolderInExplorer(target.path, target.label);
        }}
      >
        <span className="project-context-icon">
          <IconFolderOpen size={14} />
        </span>
        <span>
          <b>在檔案總管中開啟</b>
          <small>開啟此{menu.isFolder ? "資料夾" : "專案"}位置</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={projectTransferBusy}
        onClick={() => {
          const target = menu;
          onClose();
          exportProjectArchive(target);
        }}
      >
        <span className="project-context-icon">
          <IconExport size={14} />
        </span>
        <span>
          <b>匯出封裝</b>
          <small>
            {menu.isFolder || menu.childCount > 0
              ? ".veproj，含其下所有子專案"
              : ".veproj，含節點、子頁與執行紀錄"}
          </small>
        </span>
      </button>
      <div className="project-context-separator" />
      {/* The workspace menu offers the same five actions against the workspace
          root or the active project; here they act on the row that was clicked,
          which is the only way to build under — or import into — a project
          without opening it first. "." is the root project: it names the
          workspace root everywhere else too (ProjectCreatePanel maps that
          parent onto the top level), so these stay enabled for it, unlike the
          rename/move/remove items below which have no row of their own to act
          on. */}
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          beginProjectCreate({ mode: "project", parent: menu.name });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconPlus size={14} />
        </span>
        <span>
          <b>新增子專案</b>
          <small>建立在這個專案底下</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => {
          beginProjectCreate({ mode: "folder", parent: menu.name });
          onClose();
        }}
      >
        <span className="project-context-icon">
          <IconFolderOpen size={14} />
        </span>
        <span>
          <b>新增子資料夾</b>
          <small>純資料夾，建立在這裡用來分組</small>
        </span>
      </button>
      <div className="project-context-separator" />
      <button
        type="button"
        role="menuitem"
        disabled={transfer.projectTransferBusy}
        onClick={() => {
          const target = menu.name;
          onClose();
          transfer.beginImport(target, "archive");
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入專案</b>
          <small>.veproj 或 .zip，放進這個專案底下</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={transfer.markdownImportBusy}
        onClick={() => {
          const target = menu.name;
          onClose();
          transfer.beginImport(target, "markdown");
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入 MD</b>
          <small>把 Markdown 檔加入這個專案</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={transfer.jsonCanvasBusy}
        onClick={() => {
          const target = menu.name;
          onClose();
          transfer.beginImport(target, "canvas");
        }}
      >
        <span className="project-context-icon">
          <IconImport size={14} />
        </span>
        <span>
          <b>匯入 Canvas</b>
          <small>Obsidian JSON Canvas（.canvas），加入這個專案</small>
        </span>
      </button>
      <div className="project-context-separator" />
      {/* Dragging a row onto another one already moves it, and on a phone that
          gesture is close to impossible: the tree scrolls under the finger and
          there is no second hand to hold it still. This is the same operation
          with a destination picker instead of a drop target. */}
      <button
        type="button"
        role="menuitem"
        disabled={projectRenameBusy || menu.name === "."}
        onClick={() => onMove(menu)}
      >
        <span className="project-context-icon">
          <IconFolder size={14} />
        </span>
        <span>
          <b>
            {menu.names.length > 1 ? `搬移 ${menu.names.length} 個…` : "搬移到…"}
          </b>
          <small>改變它在工作區裡的位置</small>
        </span>
      </button>
      <button
        type="button"
        role="menuitem"
        disabled={projectRenameBusy || menu.name === "."}
        onClick={() => onRename(menu)}
      >
        <span className="project-context-icon" aria-hidden>
          ✎
        </span>
        <span>
          <b>重新命名</b>
          <small>
            修改{menu.isFolder ? "資料夾" : "專案"}名稱
          </small>
        </span>
      </button>
      <div className="project-context-separator" />
      <button
        type="button"
        role="menuitem"
        disabled={projectRemoveBusy || menu.name === "."}
        onClick={() => void removeProject("detach")}
      >
        <span className="project-context-icon">
          <IconClose size={14} />
        </span>
        <span>
          <b>
            {menu.names.length > 1
              ? `解除匯入 ${menu.names.length} 個`
              : "解除匯入"}
          </b>
          <small>從清單移除，保留磁碟檔案</small>
        </span>
      </button>
      <div className="project-context-separator" />
      <button
        type="button"
        role="menuitem"
        className="danger"
        disabled={projectRemoveBusy || menu.name === "."}
        onClick={() => void removeProject("disk")}
      >
        <span className="project-context-icon">
          <IconTrash size={14} />
        </span>
        <span>
          <b>
            {menu.names.length > 1
              ? `永久刪除 ${menu.names.length} 個`
              : "永久刪除"}
          </b>
          <small>刪除專案資料夾，無法復原</small>
        </span>
      </button>
    </div>
  );
}
