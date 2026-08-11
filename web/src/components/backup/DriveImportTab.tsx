import { useCallback, useEffect, useState } from "react";
import { api, type DriveFolder, type RemoteBundle } from "../../api";
import { IconCheck, IconFolder } from "../../icons";
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
  const currentFolderName = folderStack.at(-1)?.name ?? "我的雲端硬碟";
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
      setNote({ text: reason(error, "讀取 Google Drive 失敗"), kind: "error" });
    } finally {
      setBrowseBusy(false);
    }
  }, []);

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
      text: `已從 Google Drive 匯入並開啟「${result.value}」；之後可用備份分頁回存。`,
      kind: "ok",
    });
    onImported();
  };

  if (!config?.driveAvailable) {
    return (
      <div className="drive-workspace-state">
        <strong>尚未設定 Google OAuth</strong>
        <p>請先在「備份」分頁輸入 OAuth Client ID 與 Client Secret。</p>
      </div>
    );
  }
  if (!canBrowse) {
    return (
      <div className="drive-workspace-state">
        <strong>
          {config.driveNeedsReauth ? "需要更新 Google Drive 權限" : "尚未連線 Google Drive"}
        </strong>
        <p>首次使用或舊版只授予備份權限時，需要重新授權，才能瀏覽資料夾並下載既有快照。</p>
        <a className="backup-connect" href={api.driveAuthURL("workspace")}>
          {config.driveNeedsReauth ? "重新授權 Google Drive" : "連線 Google Drive"}
        </a>
      </div>
    );
  }

  return (
    <>
      <div className="drive-workspace-browser">
        <div className="drive-workspace-toolbar">
          <button type="button" onClick={goBack} disabled={browseBusy || folderStack.length === 0}>
            上層
          </button>
          <button type="button" onClick={goRoot} disabled={browseBusy}>
            根目錄
          </button>
          <span title={currentFolderName}>{currentFolderName}</span>
        </div>

        <div className="drive-workspace-columns">
          <section>
            <h3>資料夾</h3>
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
                <li className="drive-workspace-empty">沒有子資料夾</li>
              )}
            </ul>
          </section>
          <section>
            <h3>Nodevas 快照</h3>
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
                  此資料夾沒有 Nodevas 快照（只會列出由 Nodevas 備份產生的檔案）
                </li>
              )}
            </ul>
          </section>
        </div>
        {browseBusy && <p className="drive-workspace-loading">讀取 Drive 中…</p>}
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
          {busy ? "匯入中…" : "匯入並開啟"}
        </button>
      </footer>
    </>
  );
}
