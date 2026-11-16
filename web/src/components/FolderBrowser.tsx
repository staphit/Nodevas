/**
 * A server-side folder browser: where it is, a way up, the folders in it and
 * "New folder here". It only ever lists folders. The listing and the new
 * folder come from the caller (GET /api/fs/dirs, POST /api/fs/mkdir), so the
 * workspace picker and "Add repo" share it while each keeps its own
 * selection. A caller that picks a program file ("Show Nodevas where Claude
 * is") also passes the files the listing named, and gets file rows below the
 * folders; without them it only ever lists folders.
 */

import { useState } from "react";

import { IconCheck, IconDoc, IconFolder, IconFolderOpen, IconPlus } from "../icons";
import { useI18n } from "../i18n";

/** One answer of GET /api/fs/dirs: "" path is the list of drives. */
export interface FolderListing {
  path: string;
  parent: string;
  dirs: { name: string; path: string }[] | null;
}

export function FolderBrowser({
  listing,
  selected,
  busy = false,
  onBrowse,
  createFolder,
  onCreated,
  onError,
  home,
  files,
  onPickFile,
  pickedFile = "",
  emptyFilesText,
}: {
  /** null while the first listing loads. */
  listing: FolderListing | null;
  /** Whether the folder shown is the one picked. */
  selected: boolean;
  /** Something is being done with the pick: browsing waits. */
  busy?: boolean;
  onBrowse: (path: string) => void;
  /** Creates `name` in `parent` and answers its path; without it there is no
   * "New folder here". */
  createFolder?: (parent: string, name: string) => Promise<{ path: string }>;
  /** A folder was created: the caller usually browses into it. */
  onCreated?: (path: string) => void;
  /** A message for the caller's error line, or null to clear it. */
  onError: (message: string | null) => void;
  /** The person's home folder: a "My folder" button goes there. */
  home?: string;
  /** Files of the folder shown to pick from; undefined lists folders only. */
  files?: { name: string; path: string }[] | null;
  onPickFile?: (path: string) => void;
  /** The path of the file picked, "" for none. */
  pickedFile?: string;
  /** Said when `files` is empty. */
  emptyFilesText?: string;
}) {
  const { t } = useI18n();
  const [newOpen, setNewOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const isSelected = Boolean(listing?.path) && selected;

  const create = async () => {
    const parent = listing?.path;
    const name = newName.trim();
    if (!parent || !createFolder || creating || busy) return;
    if (!name) {
      onError(t("explorer.folderNameRequired"));
      return;
    }
    if (name === "." || name === ".." || /[\\/:*?"<>|]/.test(name)) {
      onError(t("explorer.folderNameInvalid"));
      return;
    }
    setCreating(true);
    onError(null);
    try {
      const result = await createFolder(parent, name);
      setNewName("");
      setNewOpen(false);
      onCreated?.(result.path);
    } catch (error) {
      onError((error as Error).message || t("explorer.createFolderFailed"));
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="fs-browser">
      <div className="fs-browser-head">
        <button
          type="button"
          onClick={() => onBrowse(listing?.parent ?? "")}
          disabled={!listing || busy}
          title={t("explorer.parentFolder")}
          aria-label={t("explorer.parentFolder")}
        >
          ↰
        </button>
        <span className={`fs-browser-path${isSelected ? " selected" : ""}`} title={listing?.path || ""}>
          {listing ? listing.path || t("explorer.chooseDrive") : t("explorer.loading")}
        </span>
        <span className={`fs-browser-selection${isSelected ? " selected" : ""}`} aria-live="polite">
          {isSelected ? (
            <>
              <IconCheck size={12} />
              {t("explorer.selected")}
            </>
          ) : (
            t("explorer.notSelected")
          )}
        </span>
      </div>
      {home && (
        <div className="fs-browser-toolbar">
          <button type="button" disabled={busy} onClick={() => onBrowse(home)}>
            <IconFolderOpen size={12} />
            {t("local.addAgent.home")}
          </button>
        </div>
      )}
      {createFolder && (
        <div className="fs-browser-toolbar">
          <button
            type="button"
            disabled={!listing?.path || busy || creating}
            onClick={() => {
              setNewOpen((open) => !open);
              setNewName("");
              onError(null);
            }}
            aria-expanded={newOpen}
          >
            <IconPlus size={12} />
            {newOpen ? t("explorer.collapseNewFolder") : t("explorer.newFolderHere")}
          </button>
        </div>
      )}
      {createFolder && newOpen && (
        <div className="fs-browser-create">
          <IconFolder size={13} />
          <input
            value={newName}
            placeholder={t("explorer.newFolderName")}
            aria-label={t("explorer.newFolderName")}
            disabled={creating}
            autoFocus
            autoComplete="off"
            onChange={(event) => {
              setNewName(event.target.value);
              onError(null);
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void create();
              } else if (event.key === "Escape") {
                setNewOpen(false);
                setNewName("");
              }
            }}
          />
          <button type="button" className="primary" disabled={!newName.trim() || creating} onClick={() => void create()}>
            {creating ? t("explorer.creating") : t("explorer.create")}
          </button>
          <button
            type="button"
            disabled={creating}
            onClick={() => {
              setNewOpen(false);
              setNewName("");
            }}
          >
            {t("common.cancel")}
          </button>
        </div>
      )}
      <ul className="fs-browser-list">
        {(listing?.dirs ?? []).map((dir) => (
          <li key={dir.path}>
            <button type="button" onClick={() => onBrowse(dir.path)} disabled={busy}>
              <IconFolder size={12} /> {dir.name}
            </button>
          </li>
        ))}
        {listing && (listing.dirs ?? []).length === 0 && <li className="fs-browser-empty">{t("explorer.noSubfolders")}</li>}
      </ul>
      {files !== undefined && (
        <ul className="fs-browser-list fs-browser-files">
          {(files ?? []).map((file) => (
            <li key={file.path}>
              <button
                type="button"
                className={file.path === pickedFile ? "selected" : undefined}
                aria-pressed={file.path === pickedFile}
                onClick={() => onPickFile?.(file.path)}
                disabled={busy}
              >
                <IconDoc size={12} /> {file.name}
              </button>
            </li>
          ))}
          {(files ?? []).length === 0 && emptyFilesText && <li className="fs-browser-empty">{emptyFilesText}</li>}
        </ul>
      )}
    </div>
  );
}
