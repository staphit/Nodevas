/**
 * The new project / new workspace directory panel [B-06].
 *
 * Both kinds of creation share one form because they differ only in what the
 * server is asked for at the end; the validation and the placement picker are
 * the same either way.
 */

import { useEffect, useRef, useState, type FormEvent } from "react";
import { IconClose, IconFolder, IconPlus } from "../../icons";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import type { ProjectEntry } from "../../state/types";

/** `parent` is the containing project/folder path; "" means the workspace root. */
export type ProjectCreateTarget = { mode: "project" | "folder"; parent: string };

export function useProjectCreate({
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
  const switchProject = useApp((state) => state.switchProject);
  const refreshProjects = useApp((state) => state.refreshProjects);
  const { t } = useI18n();
  const [projectCreateTarget, setProjectCreateTarget] =
    useState<ProjectCreateTarget | null>(null);
  const [projectName, setProjectName] = useState("");
  const [projectTemplate, setProjectTemplate] = useState<"empty" | "eisenhower">("empty");
  const [projectCreateError, setProjectCreateError] = useState<string | null>(null);
  const [projectCreateBusy, setProjectCreateBusy] = useState(false);
  const projectNameInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (projectCreateTarget) {
      requestAnimationFrame(() => projectNameInputRef.current?.focus());
    }
  }, [projectCreateTarget]);

  const beginProjectCreate = (target: ProjectCreateTarget) => {
    setProjectCreateTarget(target);
    setProjectName("");
    setProjectTemplate("empty");
    setProjectCreateError(null);
  };

  const closeProjectCreate = () => {
    if (projectCreateBusy) return;
    setProjectCreateTarget(null);
    setProjectName("");
    setProjectCreateError(null);
  };

  const createProject = async (event: FormEvent) => {
    event.preventDefault();
    if (!projectCreateTarget || projectCreateBusy) return;
    const label = projectName.trim();
    if (!label) {
      setProjectCreateError(t("explorer.createProjectRequired"));
      return;
    }
    if (label === "." || label === ".." || /[\\/]/.test(label)) {
      setProjectCreateError(t("explorer.createNameInvalid"));
      return;
    }
    if (["nodes", "run", ".vised"].includes(label.toLocaleLowerCase())) {
      setProjectCreateError(t("explorer.createReservedName"));
      return;
    }
    const fullName =
      projectCreateTarget.parent && projectCreateTarget.parent !== "."
        ? `${projectCreateTarget.parent}/${label}`
        : label;
    if (projectByName.has(fullName)) {
      setProjectCreateError(t("explorer.createDuplicate"));
      return;
    }

    setProjectCreateBusy(true);
    setProjectCreateError(null);
    try {
      if (projectCreateTarget.mode === "folder") {
        await api.createProjectFolder(fullName);
        await refreshProjects();
        setProjectTransferNotice(t("explorer.createdFolder", { name: fullName }));
      } else {
        await switchProject(fullName, true, projectTemplate);
        setProjectTransferNotice(t("explorer.createdProject", { name: fullName }));
      }
      const next = new Set(expandedProjects);
      next.add(fullName);
      if (projectCreateTarget.parent) next.add(projectCreateTarget.parent);
      persistExpandedProjects(next);
      setProjectCreateTarget(null);
      setProjectName("");
    } catch (error) {
      setProjectCreateError((error as Error).message || t("explorer.createFailed"));
    } finally {
      setProjectCreateBusy(false);
    }
  };

  return {
    projectCreateTarget,
    setProjectCreateTarget,
    projectName,
    setProjectName,
    projectTemplate,
    setProjectTemplate,
    projectCreateError,
    setProjectCreateError,
    projectCreateBusy,
    projectNameInputRef,
    beginProjectCreate,
    closeProjectCreate,
    createProject,
  };
}

export type ProjectCreate = ReturnType<typeof useProjectCreate>;

export function ProjectCreatePanel({
  create,
  projects,
  projectByName,
}: {
  create: ProjectCreate;
  projects: ProjectEntry[];
  projectByName: Map<string, ProjectEntry>;
}) {
  const {
    projectCreateTarget,
    setProjectCreateTarget,
    projectName,
    setProjectName,
    projectTemplate,
    setProjectTemplate,
    projectCreateError,
    setProjectCreateError,
    projectCreateBusy,
    projectNameInputRef,
    closeProjectCreate,
    createProject,
  } = create;
  const { t } = useI18n();
  if (!projectCreateTarget) return null;

  return (
    <form className="project-create-panel" onSubmit={createProject}>
      <div className="project-create-heading">
        <span className="project-create-icon">
          {projectCreateTarget.mode === "project" ? (
            <IconPlus size={15} />
          ) : (
            <IconFolder size={15} />
          )}
        </span>
        <div>
          <strong>
            {projectCreateTarget.mode === "folder"
              ? t("explorer.projectCreateFolder")
              : projectCreateTarget.parent
                ? t("explorer.projectCreateChild")
                : t("explorer.projectCreateNew")}
          </strong>
          <span>
            {projectCreateTarget.mode === "folder"
              ? t("explorer.projectCreateFolderHint")
              : projectCreateTarget.parent
                ? t("explorer.projectCreateUnder", {
                    parent:
                      projectByName.get(projectCreateTarget.parent)?.label ||
                      projectCreateTarget.parent,
                  })
                : t("explorer.projectCreateRootHint")}
          </span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeProjectCreate}
          aria-label={t("explorer.closeCreatePanel")}
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-parent">{t("explorer.parentLocation")}</label>
      <select
        id="project-parent"
        value={projectCreateTarget.parent}
        disabled={projectCreateBusy}
        onChange={(event) => {
          setProjectCreateTarget({
            mode: projectCreateTarget.mode,
            parent: event.target.value,
          });
          setProjectCreateError(null);
        }}
      >
        <option value="">{t("explorer.workspaceRoot")}</option>
        {projects.map((project) => (
          <option key={project.name} value={project.name}>
            {" ".repeat(Math.max(0, project.depth) * 2)}{project.isFolder ? "📁 " : ""}
            {project.label}
          </option>
        ))}
      </select>
      <label htmlFor="project-name">
        {projectCreateTarget.mode === "folder"
          ? t("explorer.folderName")
          : t("explorer.projectName")}
      </label>
      <input
        ref={projectNameInputRef}
        id="project-name"
        value={projectName}
        onChange={(event) => {
          setProjectName(event.target.value);
          setProjectCreateError(null);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") closeProjectCreate();
        }}
        placeholder={
          projectCreateTarget.parent
            ? t("explorer.projectExampleChild")
            : t("explorer.projectExampleRoot")
        }
        aria-invalid={Boolean(projectCreateError)}
        aria-describedby={
          projectCreateError ? "project-create-error" : "project-create-help"
        }
        disabled={projectCreateBusy}
        autoComplete="off"
      />
      <p id="project-create-help" className="project-create-help">
        {t("explorer.idsManaged")}
      </p>
      {projectCreateTarget.mode !== "folder" && (
        <>
          <label htmlFor="project-template">{t("explorer.template")}</label>
          <select
            id="project-template"
            value={projectTemplate}
            disabled={projectCreateBusy}
            onChange={(event) =>
              setProjectTemplate(event.target.value as "empty" | "eisenhower")
            }
          >
            <option value="empty">{t("explorer.emptyProject")}</option>
            <option value="eisenhower">{t("explorer.eisenhowerTemplate")}</option>
          </select>
        </>
      )}
      {projectCreateError && (
        <p id="project-create-error" className="project-create-error" role="alert">
          {projectCreateError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={closeProjectCreate}>
          {t("common.cancel")}
        </button>
        <button className="primary" type="submit" disabled={projectCreateBusy}>
          {projectCreateBusy
            ? t("explorer.creating")
            : projectCreateTarget.mode === "folder"
              ? t("explorer.createFolder")
              : t("explorer.createAndOpen")}
        </button>
      </div>
    </form>
  );
}
