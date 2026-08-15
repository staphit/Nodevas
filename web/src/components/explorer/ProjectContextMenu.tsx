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
import { useI18n } from "../../i18n";
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
  const { t } = useI18n();
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
    const subject = many
      ? t("explorer.projectSubjectMany", { count: targets.length })
      : t("explorer.projectSubjectOne", { label: menu.label });
    const confirmed = await confirmAction(
      mode === "disk"
        ? {
            title: t("explorer.deleteProjectsTitle", { subject }),
            description: many
              ? t("explorer.deleteProjectsDescriptionMany", { projects: targets.join("\n") })
              : t("explorer.deleteProjectDescription", {
                  path: menu.path,
                  children: hasChildren
                    ? t("explorer.childProjectCount", { count: menu.childCount })
                    : "",
                }),
            confirmLabel: t("explorer.permanentlyDelete"),
            tone: "danger",
          }
        : {
            title: t("explorer.detachProjectsTitle", { subject }),
            description: many
              ? t("explorer.detachProjectsDescriptionMany", { projects: targets.join("\n") })
              : t("explorer.detachProjectDescription", {
                  children: hasChildren
                    ? t("explorer.hiddenChildProjects", { count: menu.childCount })
                    : "",
                }),
            confirmLabel: t("explorer.detach"),
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
          failed.push(`${name}: ${(error as Error).message}`);
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
      const what = mode === "disk" ? t("explorer.permanentlyDelete") : t("explorer.detached");
      if (failed.length === 0) {
        setProjectTransferNotice(
          many
            ? t("explorer.removedMany", { action: what, count: removed.length })
            : t("explorer.removedOne", {
                action: what,
                label: menu.label,
                suffix: mode === "disk" ? "" : t("explorer.filesKeptSuffix"),
              }),
        );
      } else {
        setProjectTransferNotice(
          t("explorer.removedPartial", {
            action: what,
            removed: removed.length,
            failed: failed.length,
            details: failed.join("; "),
          }),
        );
      }
    } catch (error) {
      setProjectTransferNotice(t("explorer.projectRemoveFailed", { error: (error as Error).message }));
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
          ? t("explorer.projectActions", { count: menu.names.length })
          : t("explorer.singleProjectActions", { label: menu.label })
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
          <b>{t("explorer.openInExplorer")}</b>
          <small>{t("explorer.openLocation", { kind: menu.isFolder ? t("explorer.folder") : t("explorer.project") })}</small>
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
          <b>{t("explorer.projectArchive")}</b>
          <small>
            {menu.isFolder || menu.childCount > 0
              ? t("explorer.projectArchiveChildren")
              : t("explorer.projectArchiveContents")}
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
          <b>{t("explorer.newChildProject")}</b>
          <small>{t("explorer.projectChildHint")}</small>
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
          <b>{t("explorer.newWorkspaceFolder")}</b>
          <small>{t("explorer.folderChildHint")}</small>
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
          <b>{t("explorer.importArchive")}</b>
          <small>{t("explorer.importProjectInto")}</small>
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
          <b>{t("explorer.importMarkdown")}</b>
          <small>{t("explorer.importMarkdownInto")}</small>
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
          <b>{t("explorer.importCanvas")}</b>
          <small>{t("explorer.importCanvasInto")}</small>
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
            {menu.names.length > 1
              ? t("explorer.moveMany", { count: menu.names.length })
              : t("explorer.move")}
          </b>
          <small>{t("explorer.moveHint")}</small>
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
          <b>{t("explorer.rename")}</b>
          <small>
            {t("explorer.projectRenameHint", {
              kind: menu.isFolder ? t("explorer.folder") : t("explorer.project"),
            })}
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
              ? t("explorer.detachMany", { count: menu.names.length })
              : t("explorer.detach")}
          </b>
          <small>{t("explorer.detachHint")}</small>
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
              ? t("explorer.projectDeleteMany", { count: menu.names.length })
              : t("explorer.projectDelete")}
          </b>
          <small>{t("explorer.projectDeleteHint")}</small>
        </span>
      </button>
    </div>
  );
}
