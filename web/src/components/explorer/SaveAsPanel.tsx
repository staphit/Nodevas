/**
 * The save-as panel for the open project [B-06].
 *
 * Save-as copies the project directory under a new name and opens the copy, so
 * the original stays exactly as it was on disk.
 */

import { useRef, useState, type FormEvent } from "react";
import { IconClose, IconFolder } from "../../icons";
import { api } from "../../api";
import { useI18n } from "../../i18n";
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
  const { t } = useI18n();
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
    setSaveAsName(`${base}${t("explorer.renameCopySuffix")}`);
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
      setSaveAsError(t("explorer.saveAsRequired"));
      return;
    }
    if (name.includes("/") || name === "." || name === "..") {
      setSaveAsError(t("explorer.saveAsInvalid"));
      return;
    }
    const parent = activeProject.includes("/")
      ? activeProject.slice(0, activeProject.lastIndexOf("/"))
      : "";
    const fullName = parent ? `${parent}/${name}` : name;
    if (projectByName.has(fullName)) {
      setSaveAsError(t("explorer.saveAsDuplicate"));
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
      setProjectTransferNotice(t("explorer.savedAs", { name: fullName }));
      closeSaveAs();
    } catch (error) {
      setSaveAsError((error as Error).message || t("explorer.saveAsFailed"));
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
  const { t } = useI18n();
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
          <strong>{t("explorer.saveAs")}</strong>
          <span>{t("explorer.saveAsSource", { project: activeProject })}</span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={closeSaveAs}
          aria-label={t("explorer.closeSaveAsPanel")}
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="project-save-as-name">{t("explorer.saveAsProjectName")}</label>
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
        {t("explorer.saveAsHint")}
      </p>
      {saveAsError && (
        <p id="project-save-as-error" className="project-create-error" role="alert">
          {saveAsError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={closeSaveAs}>
          {t("common.cancel")}
        </button>
        <button
          className="primary"
          type="submit"
          disabled={!saveAsName.trim() || saveAsBusy}
        >
          {saveAsBusy ? t("explorer.savingAs") : t("explorer.saveAsAndOpen")}
        </button>
      </div>
    </form>
  );
}
