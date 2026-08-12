/**
 * The connect-a-workspace panel [B-06].
 *
 * Picking a workspace means picking a directory, so the panel carries its own
 * server-side filesystem browser: the browser only ever lists directories, and
 * the path field and the browser stay in step because either one can set the
 * selection.
 */

import { useState, type FormEvent } from "react";
import {
  IconCheck,
  IconClose,
  IconFolder,
  IconImport,
  IconPlus,
} from "../../icons";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";

export function useWorkspacePicker({
  setProjectTransferNotice,
}: {
  setProjectTransferNotice: (notice: string | null) => void;
}) {
  const addWorkspace = useApp((state) => state.addWorkspace);
  const { t } = useI18n();
  const [importPathOpen, setImportPathOpen] = useState(false);
  const [importPathValue, setImportPathValue] = useState("");
  const [importPathBusy, setImportPathBusy] = useState(false);
  const [importPathError, setImportPathError] = useState<string | null>(null);
  const [fsNewFolderOpen, setFsNewFolderOpen] = useState(false);
  const [fsNewFolderName, setFsNewFolderName] = useState("");
  const [fsNewFolderBusy, setFsNewFolderBusy] = useState(false);

  const [fsBrowse, setFsBrowse] = useState<{
    path: string;
    parent: string;
    dirs: { name: string; path: string }[] | null;
  } | null>(null);

  const browseTo = async (path: string) => {
    try {
      const result = await api.listDirs(path);
      setFsBrowse(result);
      setImportPathValue(result.path);
      setImportPathError(null);
    } catch (error) {
      setImportPathError((error as Error).message || t("explorer.readFolderFailed"));
    }
  };

  const openWorkspacePicker = () => {
    setImportPathOpen(true);
    setImportPathValue("");
    setImportPathError(null);
    void browseTo("");
  };

  // The Drive import lives in the cloud-backup dialog, which App owns. Asking
  // App to open it (rather than mounting a copy here) is what keeps one dialog
  // on screen at a time; the OAuth return is likewise App's to handle.
  const openDriveWorkspace = () => {
    if (importPathBusy) return;
    setImportPathOpen(false);
    window.dispatchEvent(new Event("nodevas-drive-import"));
  };

  const createFSFolder = async () => {
    const parent = fsBrowse?.path;
    const name = fsNewFolderName.trim();
    if (!parent || fsNewFolderBusy || importPathBusy) return;
    if (!name) {
      setImportPathError(t("explorer.folderNameRequired"));
      return;
    }
    if (name === "." || name === ".." || /[\\/:*?"<>|]/.test(name)) {
      setImportPathError(t("explorer.folderNameInvalid"));
      return;
    }

    setFsNewFolderBusy(true);
    setImportPathError(null);
    try {
      const result = await api.createDir(parent, name);
      setFsNewFolderName("");
      setFsNewFolderOpen(false);
      setImportPathValue(result.path);
      await browseTo(result.path);
    } catch (error) {
      setImportPathError((error as Error).message || t("explorer.createFolderFailed"));
    } finally {
      setFsNewFolderBusy(false);
    }
  };

  const importFromDiskPath = async (event: FormEvent) => {
    event.preventDefault();
    if (importPathBusy) return;
    const path = importPathValue.trim();
    if (!path) {
      setImportPathError(t("explorer.chooseFolderRequired"));
      return;
    }
    setImportPathBusy(true);
    setImportPathError(null);
    try {
      const result = await addWorkspace(path);
      setProjectTransferNotice(t("explorer.workspaceAdded", { label: result.label }));
      setImportPathOpen(false);
      setImportPathValue("");
    } catch (error) {
      setImportPathError((error as Error).message || t("explorer.addWorkspaceFailed"));
    } finally {
      setImportPathBusy(false);
    }
  };

  return {
    importPathOpen,
    setImportPathOpen,
    importPathValue,
    setImportPathValue,
    importPathBusy,
    importPathError,
    setImportPathError,
    fsNewFolderOpen,
    setFsNewFolderOpen,
    fsNewFolderName,
    setFsNewFolderName,
    fsNewFolderBusy,
    fsBrowse,
    browseTo,
    openWorkspacePicker,
    openDriveWorkspace,
    createFSFolder,
    importFromDiskPath,
  };
}

export type WorkspacePicker = ReturnType<typeof useWorkspacePicker>;

export function WorkspacePickerPanel({ picker }: { picker: WorkspacePicker }) {
  const {
    importPathOpen,
    setImportPathOpen,
    importPathValue,
    setImportPathValue,
    importPathBusy,
    importPathError,
    setImportPathError,
    fsNewFolderOpen,
    setFsNewFolderOpen,
    fsNewFolderName,
    setFsNewFolderName,
    fsNewFolderBusy,
    fsBrowse,
    browseTo,
    openDriveWorkspace,
    createFSFolder,
    importFromDiskPath,
  } = picker;
  const { t } = useI18n();
  if (!importPathOpen) return null;

  return (
    <form className="project-create-panel" onSubmit={importFromDiskPath}>
      <div className="project-create-heading">
        <span className="project-create-icon">
          <IconImport size={15} />
        </span>
        <div>
          <strong>{t("explorer.pickerTitle")}</strong>
          <span>{t("explorer.pickerDescription")}</span>
        </div>
        <button
          type="button"
          className="project-create-close"
          onClick={() => setImportPathOpen(false)}
          aria-label={t("explorer.closePickerPanel")}
        >
          <IconClose size={14} />
        </button>
      </div>
      <label htmlFor="import-path-value">{t("explorer.fullFolderPath")}</label>
      <input
        id="import-path-value"
        value={importPathValue}
        placeholder={t("explorer.folderPathPlaceholder")}
        onChange={(event) => {
          setImportPathValue(event.target.value);
          setImportPathError(null);
        }}
        disabled={importPathBusy}
        autoComplete="off"
      />
      <div className="workspace-drive-connect">
        <span>{t("explorer.driveQuestion")}</span>
        <button type="button" onClick={openDriveWorkspace}>
          {t("explorer.useDriveOAuth")}
        </button>
        <small>{t("explorer.driveHint")}</small>
      </div>
      <div className="fs-browser">
        <div className="fs-browser-head">
          <button
            type="button"
            onClick={() => void browseTo(fsBrowse?.parent ?? "")}
            disabled={!fsBrowse || importPathBusy}
            title={t("explorer.parentFolder")}
          >
            ↰
          </button>
          <span
            className={`fs-browser-path${
              fsBrowse?.path &&
              importPathValue.trim() === fsBrowse.path
                ? " selected"
                : ""
            }`}
            title={fsBrowse?.path || ""}
          >
            {fsBrowse ? fsBrowse.path || t("explorer.chooseDrive") : t("explorer.loading")}
          </span>
          <span
            className={`fs-browser-selection${
              fsBrowse?.path &&
              importPathValue.trim() === fsBrowse.path
                ? " selected"
                : ""
            }`}
            aria-live="polite"
          >
            {fsBrowse?.path &&
            importPathValue.trim() === fsBrowse.path ? (
              <>
                <IconCheck size={12} />
                {t("explorer.selected")}
              </>
            ) : (
              t("explorer.notSelected")
            )}
          </span>
        </div>
        <div className="fs-browser-toolbar">
          <button
            type="button"
            disabled={!fsBrowse?.path || importPathBusy || fsNewFolderBusy}
            onClick={() => {
              setFsNewFolderOpen((open) => !open);
              setFsNewFolderName("");
              setImportPathError(null);
            }}
            aria-expanded={fsNewFolderOpen}
          >
            <IconPlus size={12} />
            {fsNewFolderOpen
              ? t("explorer.collapseNewFolder")
              : t("explorer.newFolderHere")}
          </button>
        </div>
        {fsNewFolderOpen && (
          <div className="fs-browser-create">
            <IconFolder size={13} />
            <input
              value={fsNewFolderName}
              placeholder={t("explorer.newFolderName")}
              aria-label={t("explorer.newFolderName")}
              disabled={fsNewFolderBusy}
              autoFocus
              autoComplete="off"
              onChange={(event) => {
                setFsNewFolderName(event.target.value);
                setImportPathError(null);
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void createFSFolder();
                } else if (event.key === "Escape") {
                  setFsNewFolderOpen(false);
                  setFsNewFolderName("");
                }
              }}
            />
            <button
              type="button"
              className="primary"
              disabled={!fsNewFolderName.trim() || fsNewFolderBusy}
              onClick={() => void createFSFolder()}
            >
              {fsNewFolderBusy ? t("explorer.creating") : t("explorer.create")}
            </button>
            <button
              type="button"
              disabled={fsNewFolderBusy}
              onClick={() => {
                setFsNewFolderOpen(false);
                setFsNewFolderName("");
              }}
            >
              {t("common.cancel")}
            </button>
          </div>
        )}
        <ul className="fs-browser-list">
          {(fsBrowse?.dirs ?? []).map((dir) => (
            <li key={dir.path}>
              <button
                type="button"
                onClick={() => void browseTo(dir.path)}
                disabled={importPathBusy}
              >
                <IconFolder size={12} /> {dir.name}
              </button>
            </li>
          ))}
          {fsBrowse && (fsBrowse.dirs ?? []).length === 0 && (
            <li className="fs-browser-empty">{t("explorer.noSubfolders")}</li>
          )}
        </ul>
      </div>
      {importPathError && (
        <p className="project-create-error" role="alert">
          {importPathError}
        </p>
      )}
      <div className="project-create-footer">
        <button type="button" onClick={() => setImportPathOpen(false)}>
          {t("common.cancel")}
        </button>
        <button
          className="primary"
          type="submit"
          disabled={importPathBusy || !importPathValue.trim()}
        >
          {importPathBusy ? t("explorer.adding") : t("explorer.addAndOpen")}
        </button>
      </div>
    </form>
  );
}
