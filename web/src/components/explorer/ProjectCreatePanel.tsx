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
      setProjectCreateError("請輸入專案名稱。");
      return;
    }
    if (label === "." || label === ".." || /[\\/]/.test(label)) {
      setProjectCreateError("名稱不可包含斜線，也不可使用 . 或 ..。");
      return;
    }
    if (["nodes", "run", ".vised"].includes(label.toLocaleLowerCase())) {
      setProjectCreateError("這是系統保留名稱，請使用其他名稱。");
      return;
    }
    const fullName =
      projectCreateTarget.parent && projectCreateTarget.parent !== "."
        ? `${projectCreateTarget.parent}/${label}`
        : label;
    if (projectByName.has(fullName)) {
      setProjectCreateError("這個位置已經有同名專案。");
      return;
    }

    setProjectCreateBusy(true);
    setProjectCreateError(null);
    try {
      if (projectCreateTarget.mode === "folder") {
        await api.createProjectFolder(fullName);
        await refreshProjects();
        setProjectTransferNotice(`已建立工作區目錄 ${fullName}`);
      } else {
        await switchProject(fullName, true, projectTemplate);
        setProjectTransferNotice(`已建立並開啟 ${fullName}`);
      }
      const next = new Set(expandedProjects);
      next.add(fullName);
      if (projectCreateTarget.parent) next.add(projectCreateTarget.parent);
      persistExpandedProjects(next);
      setProjectCreateTarget(null);
      setProjectName("");
    } catch (error) {
      setProjectCreateError((error as Error).message || "建立失敗。");
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
              ? "新增工作區目錄"
              : projectCreateTarget.parent
                ? "新增子專案"
                : "建立新專案"}
          </strong>
          <span>
            {projectCreateTarget.mode === "folder"
              ? "純資料夾，用來分組專案（可拖曳專案進去）"
              : projectCreateTarget.parent
                ? `建立在 ${
                    projectByName.get(projectCreateTarget.parent)?.label ||
                    projectCreateTarget.parent
                  } 底下`
                : "建立在工作區根目錄"}
          </span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeProjectCreate}
          aria-label="關閉建立面板"
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-parent">上層位置</label>
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
        <option value="">工作區根目錄</option>
        {projects.map((project) => (
          <option key={project.name} value={project.name}>
            {" ".repeat(Math.max(0, project.depth) * 2)}{project.isFolder ? "📁 " : ""}
            {project.label}
          </option>
        ))}
      </select>
      <label htmlFor="project-name">
        {projectCreateTarget.mode === "folder" ? "目錄名稱" : "專案名稱"}
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
          projectCreateTarget.parent ? "例如：角色設定" : "例如：第二季企劃"
        }
        aria-invalid={Boolean(projectCreateError)}
        aria-describedby={
          projectCreateError ? "project-create-error" : "project-create-help"
        }
        disabled={projectCreateBusy}
        autoComplete="off"
      />
      <p id="project-create-help" className="project-create-help">
        ID 與資料夾路徑由系統管理。
      </p>
      {projectCreateTarget.mode !== "folder" && (
        <>
          <label htmlFor="project-template">範本</label>
          <select
            id="project-template"
            value={projectTemplate}
            disabled={projectCreateBusy}
            onChange={(event) =>
              setProjectTemplate(event.target.value as "empty" | "eisenhower")
            }
          >
            <option value="empty">空專案</option>
            <option value="eisenhower">艾森豪矩陣（四象限底圖）</option>
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
          取消
        </button>
        <button className="primary" type="submit" disabled={projectCreateBusy}>
          {projectCreateBusy
            ? "建立中…"
            : projectCreateTarget.mode === "folder"
              ? "建立目錄"
              : "建立並開啟"}
        </button>
      </div>
    </form>
  );
}
