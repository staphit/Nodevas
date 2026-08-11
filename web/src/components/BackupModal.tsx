import { useCallback, useEffect, useState } from "react";
import { api, type RemoteKind } from "../api";
import { driveReady } from "../store";
import { IconClose, IconCloud } from "../icons";
import { DriveCredentialsPanel } from "./backup/DriveCredentialsPanel";
import { DriveFolderPicker } from "./backup/DriveFolderPicker";
import { DriveImportTab } from "./backup/DriveImportTab";
import { formatCoverage, formatSize, formatWhen } from "./backup/format";
import {
  MIN_RETAIN,
  useBackupConfig,
  type Note,
} from "./backup/useBackupConfig";
import { useDriveConnection } from "./backup/useDriveConnection";

/** The dialog's two jobs. "backup" pushes copies out; "import" brings one in. */
export type BackupTab = "backup" | "import";

/**
 * Cloud backup and Drive import in one dialog.
 *
 * They were two dialogs, and that split cost more than it organised: both
 * talked to the same Drive account, both showed the same credential panel, and
 * both could be open at once — the survivor of that arrangement was whichever
 * rendered last. One dialog with two tabs keeps the shared parts genuinely
 * shared (config, credentials, the message strip) and makes "which window am I
 * in" a question that cannot come up.
 *
 * The server is always the single writer; backup and import are never on the
 * write path.
 */
export function BackupModal({
  onClose,
  initialTab = "backup",
}: {
  onClose: () => void;
  initialTab?: BackupTab;
}) {
  const [tab, setTab] = useState<BackupTab>(initialTab);
  const backup = useBackupConfig();
  const { busy, config, draft, patch, note, setNote } = backup;

  const reportDriveFailure = useCallback(
    (text: string) => setNote({ text, kind: "error" }),
    [setNote],
  );
  const pickDriveFolder = useCallback(
    (driveFolderId: string) => patch({ driveFolderId }),
    [patch],
  );
  const drive = useDriveConnection({
    folderId: draft.driveFolderId,
    onPick: pickDriveFolder,
    onError: reportDriveFailure,
  });

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onClose]);

  const disconnect = async () => {
    setNote(null);
    const result = await drive.disconnect();
    setNote(
      result.ok
        ? { text: "已中斷 Google Drive 連線。", kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  };

  const connected = driveReady(config);
  const lastBackup =
    config && !config.lastBackupAt.startsWith("0001-01-01")
      ? formatWhen(config.lastBackupAt)
      : "";
  // Pushing while the form disagrees with the server would back up settings
  // nobody chose, so both buttons wait for 儲存設定.
  const pushBlocked =
    busy || !backup.canBackup || backup.dirty || (config?.kind === "drive" && !connected);

  return (
    <div className="confirm-backdrop" onClick={() => !busy && onClose()}>
      <div
        className="confirm-dialog notify-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="雲端備份"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconCloud size={17} />
          </span>
          <div>
            <h2>雲端備份</h2>
            <p>
              把專案打包成 .veproj 推送到本機資料夾或 Google Drive，
              或把 Drive 上既有的快照匯入成新專案。
              伺服器永遠是唯一寫入者，備份不在寫入路徑上。
            </p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label="關閉" disabled={busy}>
            <IconClose size={14} />
          </button>
        </header>

        <div className="backup-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "backup"}
            className={tab === "backup" ? "active" : ""}
            onClick={() => setTab("backup")}
          >
            備份
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "import"}
            className={tab === "import" ? "active" : ""}
            onClick={() => setTab("import")}
          >
            從 Drive 匯入
          </button>
        </div>

        {config ? (
          tab === "backup" ? (
            <div className="notify-form">
              <div className="notify-field">
                <label htmlFor="backup-kind">備份後端</label>
                <select
                  id="backup-kind"
                  value={draft.kind}
                  onChange={(event) => patch({ kind: event.target.value as RemoteKind })}
                >
                  <option value="">停用</option>
                  <option value="folder">本機資料夾</option>
                  <option value="drive">
                    Google Drive{config.driveAvailable ? "" : "（需設定 OAuth 憑證）"}
                  </option>
                </select>
              </div>

              {draft.kind === "folder" && (
                <div className="notify-field">
                  <label htmlFor="backup-folder">資料夾路徑</label>
                  <input
                    id="backup-folder"
                    value={draft.folder}
                    placeholder="伺服器本機上的備份資料夾"
                    onChange={(event) => patch({ folder: event.target.value })}
                  />
                </div>
              )}

              {draft.kind === "drive" && (
                <>
                  <DriveCredentialsPanel />
                  <div className="backup-drive-status">
                    {connected ? (
                      <>
                        <span className="backup-badge ok">已連線</span>
                        <button type="button" disabled={busy} onClick={() => void disconnect()}>
                          中斷連線
                        </button>
                      </>
                    ) : config.driveAvailable ? (
                      <a className="backup-connect" href={api.driveAuthURL("backup")}>
                        連線 Google Drive
                      </a>
                    ) : null}
                  </div>
                  <div className="notify-field">
                    <label htmlFor="backup-drive-folder">Drive 資料夾（選填）</label>
                    <div className="backup-drive-folder-row">
                      <input
                        id="backup-drive-folder"
                        value={draft.driveFolderId}
                        placeholder="留空存到雲端硬碟根目錄"
                        onChange={(event) => drive.selectFolderId(event.target.value)}
                      />
                      <button
                        type="button"
                        disabled={busy || drive.busy || !connected}
                        onClick={drive.openPicker}
                      >
                        瀏覽
                      </button>
                    </div>
                    <span className="backup-drive-folder-current">
                      目前：{drive.label}
                    </span>
                    {drive.open && <DriveFolderPicker drive={drive} />}
                  </div>
                </>
              )}

              {backup.canBackup && (
                <div className="backup-schedule">
                  <label className="notify-toggle">
                    <input
                      type="checkbox"
                      checked={draft.autoBackup}
                      onChange={(event) => patch({ autoBackup: event.target.checked })}
                    />
                    自動排程備份整個工作區
                  </label>
                  {draft.autoBackup && (
                    <div className="notify-field">
                      <label htmlFor="backup-interval">每隔幾小時備份</label>
                      <div className="backup-interval">
                        <input
                          id="backup-interval"
                          type="number"
                          min={1}
                          max={720}
                          value={draft.intervalHours}
                          onChange={(event) => {
                            const hours = Number(event.target.value);
                            if (Number.isFinite(hours) && hours > 0) {
                              patch({ intervalHours: Math.floor(hours) });
                            }
                          }}
                        />
                        <span>小時</span>
                      </div>
                    </div>
                  )}
                  {draft.autoBackup && (
                    <div className="notify-field">
                      <label className="notify-toggle">
                        <input
                          type="checkbox"
                          checked={draft.pruneOld}
                          onChange={(event) => patch({ pruneOld: event.target.checked })}
                        />
                        排程備份成功後清理較舊的備份
                      </label>
                      {draft.pruneOld ? (
                        <>
                          <div className="backup-interval">
                            <input
                              id="backup-retain"
                              aria-label="保留份數"
                              type="number"
                              min={MIN_RETAIN}
                              max={500}
                              value={draft.retainBundles}
                              onChange={(event) => {
                                const count = Number(event.target.value);
                                if (Number.isFinite(count) && count > 0) {
                                  patch({ retainBundles: Math.floor(count) });
                                }
                              }}
                            />
                            <span>份</span>
                          </div>
                          <p className="backup-hint">
                            保留最近 {draft.retainBundles} 份（目前 {draft.intervalHours} 小時間隔下
                            {formatCoverage(draft.retainBundles, draft.intervalHours)}）。
                            更舊的會在每次排程備份成功後刪除；最新一份永遠保留，
                            資料夾裡不是本程式建立的檔案也不會被刪除。
                            手動「立即備份」產生的備份也算在這個份數裡，但按下它本身不會刪除任何東西。
                            最少 {MIN_RETAIN} 份，填得更小會自動調整回 {MIN_RETAIN}。
                          </p>
                        </>
                      ) : (
                        <p className="backup-hint">
                          不刪除任何備份。每次排程都會新增一份完整工作區封存，
                          目前間隔下一年約 {Math.round((365 * 24) / Math.max(draft.intervalHours, 1))} 份，
                          儲存空間與還原清單都會持續增長。
                        </p>
                      )}
                    </div>
                  )}
                  <p className="backup-hint">
                    上次備份：{lastBackup || "尚未備份"}
                  </p>
                </div>
              )}

              {backup.canBackup && (
                <div className="backup-bundles">
                  <div className="backup-bundles-head">
                    <label>備份（新到舊）</label>
                    <button type="button" disabled={busy} onClick={backup.reloadBundles}>
                      重新整理
                    </button>
                  </div>
                  {backup.bundles == null ? (
                    <p className="backup-hint">載入中…</p>
                  ) : backup.bundles.length === 0 ? (
                    <p className="backup-hint">尚無備份。</p>
                  ) : (
                    <ul className="backup-list">
                      {backup.bundles.map((bundle) => (
                        <li key={bundle.id}>
                          <div className="backup-list-meta">
                            <span className="backup-list-name">{bundle.name}</span>
                            <span className="backup-list-sub">
                              {formatSize(bundle.size)} · {formatWhen(bundle.modified)}
                            </span>
                          </div>
                          <button
                            type="button"
                            disabled={busy}
                            onClick={() => void backup.restore(bundle)}
                          >
                            還原
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}

              <NoteStrip note={note} />
            </div>
          ) : (
            <DriveImportTab onImported={onClose} />
          )
        ) : (
          <div className="notify-form">
            <p>{note ? note.text : "載入中…"}</p>
          </div>
        )}

        {tab === "backup" && (
          <footer>
            <button
              type="button"
              disabled={pushBlocked}
              onClick={() => void backup.pushWorkspace()}
              title={backup.dirty ? "先儲存設定" : "所有專案與資料夾結構打包成一份"}
            >
              備份整個工作區
            </button>
            <button
              type="button"
              disabled={pushBlocked}
              onClick={() => void backup.push()}
              title={backup.dirty ? "先儲存設定" : ""}
            >
              備份目前專案
            </button>
            <button
              type="button"
              className="primary"
              disabled={busy || !backup.dirty}
              onClick={() => void backup.save()}
            >
              儲存設定
            </button>
          </footer>
        )}
      </div>
    </div>
  );
}

function NoteStrip({ note }: { note: Note }) {
  if (!note) return null;
  return (
    <div className={`notify-message ${note.kind}`} role="status">
      {note.text}
    </div>
  );
}
