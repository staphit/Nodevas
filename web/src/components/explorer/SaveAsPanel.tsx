/**
 * The save-as panel for the open project [B-06].
 *
 * Save-as copies the project directory under a new name and opens the copy, so
 * the original stays exactly as it was on disk.
 */

import { useRef, useState, type FormEvent } from "react";
import { IconClose, IconFolder } from "../../icons";
import { api } from "../../api";
import { useApp } from "../../store";
import type { ProjectEntry } from "../../state/types";

export function useSaveAs({
  activeProject,
  projectByName,
  expandedProjects,
  persistExpandedProjects,
  setProjectTransferNotice,
}: {
  activeProject: string;
  projectByName: Map<string, ProjectEntry>;
  expandedProjects: Set<string>;
  persistExpandedProjects: (next: Set<string>) => void;
  setProjectTransferNotice: (notice: string | null) => void;
}) {
  const switchProject = useApp((state) => state.switchProject);
  const saveAllTabs = useApp((state) => state.saveAllTabs);
  const [saveAsOpen, setSaveAsOpen] = useState(false);
  const [saveAsName, setSaveAsName] = useState("");
  const [saveAsError, setSaveAsError] = useState<string | null>(null);
  const [saveAsBusy, setSaveAsBusy] = useState(false);
  const saveAsInputRef = useRef<HTMLInputElement>(null);

  const openSaveAs = () => {
    if (!activeProject) return;
    const base = activeProject.split("/").at(-1) ?? activeProject;
    setSaveAsName(`${base} 副本`);
    setSaveAsError(null);
    setSaveAsOpen(true);
    requestAnimationFrame(() => saveAsInputRef.current?.select());
  };

  const closeSaveAs = () => {
    setSaveAsOpen(false);
    setSaveAsName("");
    setSaveAsError(null);
  };

  const saveProjectAs = async (event: FormEvent) => {
    event.preventDefault();
    if (saveAsBusy || !activeProject) return;
    const name = saveAsName.trim();
    if (!name) {
      setSaveAsError("請輸入新專案名稱。");
      return;
    }
    if (name.includes("/") || name === "." || name === "..") {
      setSaveAsError("名稱不能包含 / 或為 . / ..");
      return;
    }
    const parent = activeProject.includes("/")
      ? activeProject.slice(0, activeProject.lastIndexOf("/"))
      : "";
    const fullName = parent ? `${parent}/${name}` : name;
    if (projectByName.has(fullName)) {
      setSaveAsError("這個位置已經有同名專案。");
      return;
    }

    setSaveAsBusy(true);
    setSaveAsError(null);
    try {
      // Write pending edits first, or the copy silently loses them.
      await saveAllTabs();
      await api.copyProject(activeProject, name, parent, true);
      await switchProject(fullName);
      const next = new Set(expandedProjects);
      next.add(fullName);
      if (parent) next.add(parent);
      persistExpandedProjects(next);
      setProjectTransferNotice(`已另存為 ${fullName} 並開啟`);
      closeSaveAs();
    } catch (error) {
      setSaveAsError((error as Error).message || "另存新檔失敗。");
    } finally {
      setSaveAsBusy(false);
    }
  };

  return {
    saveAsOpen,
    saveAsName,
    setSaveAsName,
    saveAsError,
    setSaveAsError,
    saveAsBusy,
    saveAsInputRef,
    openSaveAs,
    closeSaveAs,
    saveProjectAs,
  };
}

export type SaveAs = ReturnType<typeof useSaveAs>;

export function SaveAsPanel({
  saveAs,
  activeProject,
}: {
  saveAs: SaveAs;
  activeProject: string;
}) {
  const {
    saveAsOpen,
    saveAsName,
    setSaveAsName,
    saveAsError,
    setSaveAsError,
    saveAsBusy,
    saveAsInputRef,
    closeSaveAs,
    saveProjectAs,
  } = saveAs;
  if (!saveAsOpen || !activeProject) return null;

  return (
    <form className="project-create-panel" onSubmit={saveProjectAs}>
      <div className="project-create-heading">
        <span className="project-create-icon">
          <IconFolder size={15} />
        </span>
        <div>
          <strong>另存新檔</strong>
          <span>來源：{activeProject}</span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeSaveAs}
          aria-label="關閉另存新檔面板"
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-save-as-name">新專案名稱</label>
      <input
        ref={saveAsInputRef}
        id="project-save-as-name"
        value={saveAsName}
        onChange={(event) => {
          setSaveAsName(event.target.value);
          setSaveAsError(null);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") closeSaveAs();
        }}
        aria-invalid={Boolean(saveAsError)}
        aria-describedby={
          saveAsError ? "project-save-as-error" : "project-save-as-help"
        }
        disabled={saveAsBusy}
        autoComplete="off"
      />
      <p id="project-save-as-help" className="project-create-help">
        複製節點、子頁與附件到同一層的新專案並開啟；草稿、歷史與垃圾桶留在原專案。
      </p>
      {saveAsError && (
        <p id="project-save-as-error" className="project-create-error" role="alert">
          {saveAsError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={closeSaveAs}>
          取消
        </button>
        <button
          className="primary"
          type="submit"
          disabled={!saveAsName.trim() || saveAsBusy}
        >
          {saveAsBusy ? "另存中…" : "另存並開啟"}
        </button>
      </div>
    </form>
  );
}
