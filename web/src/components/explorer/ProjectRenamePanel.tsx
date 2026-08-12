/**
 * The rename panel for a project or workspace directory [B-06].
 *
 * A rename is a directory move on disk, so the expanded-project set has to be
 * remapped onto the new path or the tree would collapse under the user.
 */

import { useEffect, useRef, useState, type FormEvent } from "react";
import { IconClose, IconFolder } from "../../icons";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { reportError, useApp } from "../../store";
import type { ProjectEntry } from "../../state/types";
import type { ProjectMenuTarget } from "./ProjectContextMenu";

export function useProjectRename({
  projectByName,
  expandedProjects,
  persistExpandedProjects,
  setProjectTransferNotice,
}: {
  projectByName: Map<string, ProjectEntry>;
  expandedProjects: Set<string>;
  persistExpandedProjects: (next: Set<string>) => void;
  setProjectTransferNotice: (notice: string | null) => void;
}) {
  const refreshProjects = useApp((state) => state.refreshProjects);
  const loadAll = useApp((state) => state.loadAll);
  const { t } = useI18n();
  const [projectRenameTarget, setProjectRenameTarget] =
    useState<ProjectMenuTarget | null>(null);
  const [projectRenameValue, setProjectRenameValue] = useState("");
  const [projectRenameError, setProjectRenameError] = useState<string | null>(null);
  const [projectRenameBusy, setProjectRenameBusy] = useState(false);
  const projectRenameInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (projectRenameTarget) {
      requestAnimationFrame(() => {
        projectRenameInputRef.current?.focus();
        projectRenameInputRef.current?.select();
      });
    }
  }, [projectRenameTarget]);

  const beginProjectRename = (target: ProjectMenuTarget) => {
    setProjectRenameTarget(target);
    setProjectRenameValue(target.label);
    setProjectRenameError(null);
  };

  const closeProjectRename = () => {
    if (projectRenameBusy) return;
    setProjectRenameTarget(null);
    setProjectRenameValue("");
    setProjectRenameError(null);
  };

  const renameProject = async (event: FormEvent) => {
    event.preventDefault();
    const target = projectRenameTarget;
    if (!target || projectRenameBusy) return;
    const nextLabel = projectRenameValue.trim();
    if (!nextLabel) {
      setProjectRenameError(t("explorer.renameRequired"));
      return;
    }
    if (
      nextLabel === "." ||
      nextLabel === ".." ||
      /[\\/:*?"<>|]/.test(nextLabel)
    ) {
      setProjectRenameError(t("explorer.renameInvalid"));
      return;
    }
    if (["nodes", "run", ".vised"].includes(nextLabel.toLocaleLowerCase())) {
      setProjectRenameError(t("explorer.renameReserved"));
      return;
    }
    if (nextLabel === target.label) {
      closeProjectRename();
      return;
    }
    const separator = target.name.lastIndexOf("/");
    const parent = separator >= 0 ? target.name.slice(0, separator) : "";
    const nextName = parent ? `${parent}/${nextLabel}` : nextLabel;
    if (projectByName.has(nextName) && nextName !== target.name) {
      setProjectRenameError(t("explorer.renameDuplicate"));
      return;
    }

    setProjectRenameBusy(true);
    setProjectRenameError(null);
    try {
      const result = await api.moveProject(target.name, parent, nextLabel);
      const remapExpandedName = (name: string) =>
        name === target.name
          ? result.name
          : name.startsWith(`${target.name}/`)
            ? `${result.name}${name.slice(target.name.length)}`
            : name;
      persistExpandedProjects(
        new Set([...expandedProjects].map(remapExpandedName)),
      );
      await refreshProjects();
      if (result.active) await loadAll();
      setProjectTransferNotice(
        t("explorer.renamed", {
          kind: target.isFolder ? t("explorer.folderName") : t("explorer.projectName"),
          oldLabel: target.label,
          newLabel: nextLabel,
        }),
      );
      setProjectRenameTarget(null);
      setProjectRenameValue("");
    } catch (error) {
      setProjectRenameError(
        error instanceof Error ? error.message : t("explorer.renameFailed"),
      );
      reportError(error);
    } finally {
      setProjectRenameBusy(false);
    }
  };

  return {
    projectRenameTarget,
    projectRenameValue,
    setProjectRenameValue,
    projectRenameError,
    setProjectRenameError,
    projectRenameBusy,
    projectRenameInputRef,
    beginProjectRename,
    closeProjectRename,
    renameProject,
  };
}

export type ProjectRename = ReturnType<typeof useProjectRename>;

export function ProjectRenamePanel({ rename }: { rename: ProjectRename }) {
  const {
    projectRenameTarget,
    projectRenameValue,
    setProjectRenameValue,
    projectRenameError,
    setProjectRenameError,
    projectRenameBusy,
    projectRenameInputRef,
    closeProjectRename,
    renameProject,
  } = rename;
  const { t } = useI18n();
  if (!projectRenameTarget) return null;

  return (
    <form className="project-create-panel" onSubmit={renameProject}>
      <div className="project-create-heading">
        <span className="project-create-icon">
          <IconFolder size={15} />
        </span>
        <div>
          <strong>
            {t("explorer.renameTitle", {
              kind: projectRenameTarget.isFolder
                ? t("explorer.folderName")
                : t("explorer.projectName"),
            })}
          </strong>
          <span>{t("explorer.currentLocation", { path: projectRenameTarget.path })}</span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeProjectRename}
          aria-label={t("explorer.closeRenamePanel")}
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-rename-name">{t("explorer.newName")}</label>
      <input
        ref={projectRenameInputRef}
        id="project-rename-name"
        value={projectRenameValue}
        onChange={(event) => {
          setProjectRenameValue(event.target.value);
          setProjectRenameError(null);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") closeProjectRename();
        }}
        aria-invalid={Boolean(projectRenameError)}
        aria-describedby={
          projectRenameError ? "project-rename-error" : "project-rename-help"
        }
        disabled={projectRenameBusy}
        autoComplete="off"
      />
      <p id="project-rename-help" className="project-create-help">
        {t("explorer.projectRenameHintText")}
      </p>
      {projectRenameError && (
        <p id="project-rename-error" className="project-create-error" role="alert">
          {projectRenameError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={closeProjectRename}>
          {t("common.cancel")}
        </button>
        <button
          className="primary"
          type="submit"
          disabled={!projectRenameValue.trim() || projectRenameBusy}
        >
          {projectRenameBusy ? t("explorer.renaming") : t("explorer.rename")}
        </button>
      </div>
    </form>
  );
}
