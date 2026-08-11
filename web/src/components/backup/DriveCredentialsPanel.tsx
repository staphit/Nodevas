import { useEffect, useState } from "react";
import { remoteScope, useApp, useOperationPending } from "../../store";
import { reason } from "./format";
import type { Note } from "./useBackupConfig";

/**
 * The Google OAuth client the whole Drive feature runs on.
 *
 * Whether credentials exist is what decides `driveAvailable` on the backup
 * config, so both live in the store: saving them here has to be visible to the
 * backend picker two fields up, immediately and without a reopen.
 */
export function DriveCredentialsPanel() {
  const status = useApp((state) => state.driveCredentials);
  const refresh = useApp((state) => state.refreshDriveCredentials);
  const saveCredentials = useApp((state) => state.saveDriveCredentials);
  const clearCredentials = useApp((state) => state.clearDriveCredentials);
  const busy = useOperationPending(remoteScope.drive());

  const [editing, setEditing] = useState(false);
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [note, setNote] = useState<Note>(null);

  useEffect(() => {
    void refresh().catch((error: unknown) =>
      setNote({ text: reason(error, "讀取 OAuth 設定失敗"), kind: "error" }),
    );
  }, [refresh]);

  const save = async () => {
    if (!clientId.trim() || !clientSecret.trim()) {
      setNote({ text: "Client ID 與 Client Secret 都必須填寫。", kind: "error" });
      return;
    }
    setNote(null);
    const result = await saveCredentials(clientId, clientSecret);
    if (!result.ok) {
      setNote({ text: result.message, kind: "error" });
      return;
    }
    // The secret is never shown again, so it is not kept around either.
    setClientSecret("");
    setEditing(false);
    setNote({ text: "OAuth 憑證已加密儲存於 App secrets。", kind: "ok" });
  };

  const clear = async () => {
    setNote(null);
    const result = await clearCredentials();
    if (!result.ok) {
      setNote({ text: result.message, kind: "error" });
      return;
    }
    setEditing(false);
    setNote({ text: "已清除 App 儲存的 OAuth 憑證。", kind: "ok" });
  };

  if (!status) {
    return <p className="backup-hint">讀取 OAuth 設定中…</p>;
  }

  return (
    <div className="drive-credentials-panel">
      <div className="backup-drive-status">
        <span className={`backup-badge ${status.configured ? "ok" : ""}`}>
          {status.configured
            ? status.source === "environment"
              ? "已由環境設定"
              : "已設定"
            : "尚未設定"}
        </span>
        {!editing && status.configured && (
          <button type="button" disabled={busy} onClick={() => setEditing(true)}>
            更新憑證
          </button>
        )}
        {!editing && status.source === "app" && (
          <button type="button" disabled={busy} onClick={() => void clear()}>
            清除 App 憑證
          </button>
        )}
      </div>

      {(!status.configured || editing) && (
        <div className="drive-credentials-form">
          <div className="notify-field">
            <label htmlFor="drive-client-id">Google OAuth Client ID</label>
            <input
              id="drive-client-id"
              value={clientId}
              autoComplete="off"
              placeholder="例如：123456789.apps.googleusercontent.com"
              onChange={(event) => setClientId(event.target.value)}
            />
          </div>
          <div className="notify-field">
            <label htmlFor="drive-client-secret">Google OAuth Client Secret</label>
            <input
              id="drive-client-secret"
              type="password"
              value={clientSecret}
              autoComplete="new-password"
              onChange={(event) => setClientSecret(event.target.value)}
            />
          </div>
          <div className="backup-drive-status">
            <button type="button" className="primary" disabled={busy} onClick={() => void save()}>
              {busy ? "儲存中…" : "儲存 OAuth 憑證"}
            </button>
            {status.configured && (
              <button type="button" disabled={busy} onClick={() => setEditing(false)}>
                取消
              </button>
            )}
          </div>
          <p className="backup-hint">
            憑證不會寫入 workspace、專案或備份檔，只會加密存放在本機 App secrets。
          </p>
        </div>
      )}

      {note && <div className={`notify-message ${note.kind}`} role="status">{note.text}</div>}
    </div>
  );
}
