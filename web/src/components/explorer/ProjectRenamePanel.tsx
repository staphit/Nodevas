/**
 * The rename panel for a project or workspace directory [B-06].
 *
 * A rename is a directory move on disk, so the expanded-project set has to be
 * remapped onto the new path or the tree would collapse under the user.
 */

import { useEffect, useRef, useState, type FormEvent } from "react";
import { IconClose, IconFolder } from "../../icons";
import { api } from "../../api";
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
      setProjectRenameError("請輸入新名稱。");
      return;
    }
    if (
      nextLabel === "." ||
      nextLabel === ".." ||
      /[\\/:*?"<>|]/.test(nextLabel)
    ) {
      setProjectRenameError("名稱不可包含路徑或 Windows 不允許的字元。");
      return;
    }
    if (["nodes", "run", ".vised"].includes(nextLabel.toLocaleLowerCase())) {
      setProjectRenameError("此名稱為系統保留名稱。");
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
      setProjectRenameError("同一層已經有相同名稱。");
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
        `已重新命名${target.isFolder ? "資料夾" : "專案"}：${target.label} → ${nextLabel}`,
      );
      setProjectRenameTarget(null);
      setProjectRenameValue("");
    } catch (error) {
      setProjectRenameError(
        error instanceof Error ? error.message : "重新命名失敗。",
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
  if (!projectRenameTarget) return null;

  return (
    <form className="project-create-panel" onSubmit={renameProject}>
      <div className="project-create-heading">
        <span className="project-create-icon">
          <IconFolder size={15} />
        </span>
        <div>
          <strong>
            重新命名{projectRenameTarget.isFolder ? "資料夾" : "專案"}
          </strong>
          <span>目前位置：{projectRenameTarget.path}</span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeProjectRename}
          aria-label="關閉重新命名面板"
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-rename-name">新名稱</label>
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
        只會更改目前名稱；子專案與目前開啟狀態會自動跟著更新。
      </p>
      {projectRenameError && (
        <p id="project-rename-error" className="project-create-error" role="alert">
          {projectRenameError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={closeProjectRename}>
          取消
        </button>
        <button
          className="primary"
          type="submit"
          disabled={!projectRenameValue.trim() || projectRenameBusy}
        >
          {projectRenameBusy ? "重新命名中…" : "重新命名"}
        </button>
      </div>
    </form>
  );
}
