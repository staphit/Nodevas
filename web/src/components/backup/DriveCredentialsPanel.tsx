import { useEffect, useState } from "react";
import { useI18n } from "../../i18n";
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
  const { t } = useI18n();
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
      setNote({ text: reason(error, t("backup.readOAuthFailed")), kind: "error" }),
    );
  }, [refresh, t]);

  const save = async () => {
    if (!clientId.trim() || !clientSecret.trim()) {
      setNote({ text: t("backup.credentialsRequired"), kind: "error" });
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
    setNote({ text: t("backup.credentialsSaved"), kind: "ok" });
  };

  const clear = async () => {
    setNote(null);
    const result = await clearCredentials();
    if (!result.ok) {
      setNote({ text: result.message, kind: "error" });
      return;
    }
    setEditing(false);
    setNote({ text: t("backup.credentialsCleared"), kind: "ok" });
  };

  if (!status) {
    return <p className="backup-hint">{t("backup.oauthLoading")}</p>;
  }

  return (
    <div className="drive-credentials-panel">
      <div className="backup-drive-status">
        <span className={`backup-badge ${status.configured ? "ok" : ""}`}>
          {status.configured
            ? status.source === "environment"
              ? t("backup.configuredByEnvironment")
              : t("backup.configured")
            : t("backup.notConfigured")}
        </span>
        {!editing && status.configured && (
          <button type="button" disabled={busy} onClick={() => setEditing(true)}>
            {t("backup.updateCredentials")}
          </button>
        )}
        {!editing && status.source === "app" && (
          <button type="button" disabled={busy} onClick={() => void clear()}>
            {t("backup.clearAppCredentials")}
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
              placeholder={t("backup.clientIdPlaceholder")}
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
              {busy ? t("backup.saving") : t("backup.saveOAuthCredentials")}
            </button>
            {status.configured && (
              <button type="button" disabled={busy} onClick={() => setEditing(false)}>
                {t("backup.cancel")}
              </button>
            )}
          </div>
          <p className="backup-hint">
            {t("backup.credentialsStorageHint")}
          </p>
        </div>
      )}

      {note && <div className={`notify-message ${note.kind}`} role="status">{note.text}</div>}
    </div>
  );
}
