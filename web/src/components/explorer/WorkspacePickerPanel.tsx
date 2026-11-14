/**
 * The connect-a-workspace panel.
 *
 * Picking a workspace means picking a directory, so the panel carries the
 * server-side folder browser (FolderBrowser): it only ever lists directories,
 * and the path field and the browser stay in step because either one can set
 * the selection.
 */

import { useState, type FormEvent } from "react";
import { IconClose, IconImport } from "../../icons";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import { FolderBrowser, type FolderListing } from "../FolderBrowser";

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

  const [fsBrowse, setFsBrowse] = useState<FolderListing | null>(null);

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
    fsBrowse,
    browseTo,
    openWorkspacePicker,
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
    fsBrowse,
    browseTo,
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
      <FolderBrowser
        listing={fsBrowse}
        selected={Boolean(fsBrowse?.path) && importPathValue.trim() === fsBrowse?.path}
        busy={importPathBusy}
        onBrowse={(path) => void browseTo(path)}
        createFolder={(parent, name) => api.createDir(parent, name)}
        onCreated={(path) => {
          setImportPathValue(path);
          void browseTo(path);
        }}
        onError={setImportPathError}
      />
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
