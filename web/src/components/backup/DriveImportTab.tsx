import { useCallback, useEffect, useState } from "react";
import { api, type DriveFolder, type RemoteBundle } from "../../api";
import { IconCheck, IconFolder } from "../../icons";
import { useI18n } from "../../i18n";
import { driveReady, remoteScope, useApp, useOperationPending } from "../../store";
import { formatSize, formatWhen, reason } from "./format";
import type { Note } from "./useBackupConfig";

/**
 * Browse the Drive account's folders, pick a Nodevas snapshot, import it as a
 * new local project and switch to it.
 *
 * Only snapshots Nodevas itself pushed appear here — the OAuth scope is
 * drive.file, so files uploaded by hand are invisible to this client, and the
 * bundle list is further restricted to the appProperties marker on the server.
 */
export function DriveImportTab({ onImported }: { onImported: () => void }) {
  const { t } = useI18n();
  const config = useApp((state) => state.remoteConfig);
  const connect = useApp((state) => state.connectDriveWorkspace);
  const busy = useOperationPending(remoteScope.drive());

  const [folderStack, setFolderStack] = useState<DriveFolder[]>([]);
  const [folders, setFolders] = useState<DriveFolder[]>([]);
  const [bundles, setBundles] = useState<RemoteBundle[]>([]);
  const [selected, setSelected] = useState<RemoteBundle | null>(null);
  const [browseBusy, setBrowseBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const currentFolderID = folderStack.at(-1)?.id ?? "root";
  const currentFolderName = folderStack.at(-1)?.name ?? t("backup.currentDriveRoot");
  const canBrowse = driveReady(config);

  const loadFolder = useCallback(async (folderID: string) => {
    setBrowseBusy(true);
    setNote(null);
    try {
      const [folderResult, bundleResult] = await Promise.all([
        api.listDriveFolders(folderID),
        api.listDriveBundles(folderID),
      ]);
      setFolders(folderResult.folders);
      setBundles(bundleResult.bundles);
      setSelected(null);
    } catch (error) {
      setFolders([]);
      setBundles([]);
      setNote({ text: reason(error, t("backup.readDriveFailed")), kind: "error" });
    } finally {
      setBrowseBusy(false);
    }
  }, [t]);

  useEffect(() => {
    if (canBrowse) void loadFolder("root");
  }, [canBrowse, loadFolder]);

  const enterFolder = (folder: DriveFolder) => {
    setFolderStack((stack) => [...stack, folder]);
    void loadFolder(folder.id);
  };

  const goBack = () => {
    if (folderStack.length === 0 || browseBusy) return;
    const next = folderStack.slice(0, -1);
    setFolderStack(next);
    void loadFolder(next.at(-1)?.id ?? "root");
  };

  const goRoot = () => {
    if (browseBusy) return;
    setFolderStack([]);
    void loadFolder("root");
  };

  const importSelected = async () => {
    if (!selected || busy) return;
    setNote(null);
    const result = await connect(currentFolderID, selected.id, selected.name);
    if (!result.ok) {
      setNote({ text: result.message, kind: "error" });
      return;
    }
    setNote({
      text: t("backup.importedFromDrive", { name: result.value }),
      kind: "ok",
    });
    onImported();
  };

  if (!config?.driveAvailable) {
    return (
      <div className="drive-workspace-state">
        <strong>{t("backup.oauthNotConfigured")}</strong>
        <p>{t("backup.oauthSetupHint")}</p>
      </div>
    );
  }
  if (!canBrowse) {
    return (
      <div className="drive-workspace-state">
        <strong>
          {config.driveNeedsReauth
            ? t("backup.driveReauthRequired")
            : t("backup.driveNotConnected")}
        </strong>
        <p>{t("backup.driveReauthHint")}</p>
        <a className="backup-connect" href={api.driveAuthURL("workspace")}>
          {config.driveNeedsReauth
            ? t("backup.reauthorizeDrive")
            : t("backup.connectDrive")}
        </a>
      </div>
    );
  }

  return (
    <>
      <div className="drive-workspace-browser">
        <div className="drive-workspace-toolbar">
          <button type="button" onClick={goBack} disabled={browseBusy || folderStack.length === 0}>
            {t("backup.parentFolder")}
          </button>
          <button type="button" onClick={goRoot} disabled={browseBusy}>
            {t("backup.rootFolder")}
          </button>
          <span title={currentFolderName}>{currentFolderName}</span>
        </div>

        <div className="drive-workspace-columns">
          <section>
            <h3>{t("backup.folders")}</h3>
            <ul className="drive-workspace-folder-list">
              {folders.map((folder) => (
                <li key={folder.id}>
                  <button type="button" disabled={browseBusy} onClick={() => enterFolder(folder)}>
                    <IconFolder size={13} />
                    <span>{folder.name}</span>
                  </button>
                </li>
              ))}
              {!browseBusy && folders.length === 0 && (
                <li className="drive-workspace-empty">{t("backup.noChildFolders")}</li>
              )}
            </ul>
          </section>
          <section>
            <h3>{t("backup.snapshots")}</h3>
            <ul className="drive-workspace-bundle-list">
              {bundles.map((bundle) => (
                <li key={bundle.id}>
                  <button
                    type="button"
                    className={selected?.id === bundle.id ? "selected" : ""}
                    disabled={browseBusy || busy}
                    onClick={() => setSelected(bundle)}
                  >
                    <span className="drive-workspace-bundle-check">
                      {selected?.id === bundle.id && <IconCheck size={12} />}
                    </span>
                    <span className="drive-workspace-bundle-meta">
                      <b>{bundle.name}</b>
                      <small>{formatSize(bundle.size)} · {formatWhen(bundle.modified)}</small>
                    </span>
                  </button>
                </li>
              ))}
              {!browseBusy && bundles.length === 0 && (
                <li className="drive-workspace-empty">
                  {t("backup.noSnapshots")}
                </li>
              )}
            </ul>
          </section>
        </div>
        {browseBusy && <p className="drive-workspace-loading">{t("backup.readingDrive")}</p>}
      </div>

      {note && (
        <div className={`notify-message ${note.kind}`} role="status">
          {note.text}
        </div>
      )}

      <footer>
        <button
          type="button"
          className="primary"
          onClick={() => void importSelected()}
          disabled={busy || !selected}
        >
          {busy ? t("backup.importing") : t("backup.importAndOpen")}
        </button>
      </footer>
    </>
  );
}
