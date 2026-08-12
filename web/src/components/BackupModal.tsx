import { useCallback, useEffect, useState } from "react";
import { api, type RemoteKind } from "../api";
import { useI18n } from "../i18n";
import { driveReady } from "../store";
import { IconClose, IconCloud } from "../icons";
import { DriveCredentialsPanel } from "./backup/DriveCredentialsPanel";
import { DriveFolderPicker } from "./backup/DriveFolderPicker";
import { DriveImportTab } from "./backup/DriveImportTab";
import { formatSize, formatWhen } from "./backup/format";
import {
  MIN_RETAIN,
  useBackupConfig,
  type Note,
} from "./backup/useBackupConfig";
import { DRIVE_ROOT_LABEL, useDriveConnection } from "./backup/useDriveConnection";

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
  const { t } = useI18n();
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
        ? { text: t("backup.disconnected"), kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  };

  const connected = driveReady(config);
  const lastBackup =
    config && !config.lastBackupAt.startsWith("0001-01-01")
      ? formatWhen(config.lastBackupAt)
      : "";
  const driveLabel = drive.label === DRIVE_ROOT_LABEL ? t("backup.driveRoot") : drive.label;
  const coverage = formatLocalizedCoverage(draft.retainBundles, draft.intervalHours, t);
  // Pushing while the form disagrees with the server would back up settings
  // nobody chose, so both buttons wait for the saved settings.
  const pushBlocked =
    busy || !backup.canBackup || backup.dirty || (config?.kind === "drive" && !connected);

  return (
    <div className="confirm-backdrop" onClick={() => !busy && onClose()}>
      <div
        className="confirm-dialog notify-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={t("backup.title")}
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconCloud size={17} />
          </span>
          <div>
            <h2>{t("backup.title")}</h2>
            <p>
              {t("backup.description")}
            </p>
          </div>
          <button type="button" className="confirm-dialog-close" onClick={onClose} aria-label={t("backup.close")} disabled={busy}>
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
            {t("backup.tab.backup")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "import"}
            className={tab === "import" ? "active" : ""}
            onClick={() => setTab("import")}
          >
            {t("backup.tab.import")}
          </button>
        </div>

        {config ? (
          tab === "backup" ? (
            <div className="notify-form">
              <div className="notify-field">
                <label htmlFor="backup-kind">{t("backup.backend")}</label>
                <select
                  id="backup-kind"
                  value={draft.kind}
                  onChange={(event) => patch({ kind: event.target.value as RemoteKind })}
                >
                  <option value="">{t("backup.disabled")}</option>
                  <option value="folder">{t("backup.localFolder")}</option>
                  <option value="drive">
                    Google Drive{config.driveAvailable ? "" : t("backup.googleDriveOAuthRequired")}
                  </option>
                </select>
              </div>

              {draft.kind === "folder" && (
                <div className="notify-field">
                  <label htmlFor="backup-folder">{t("backup.folderPath")}</label>
                  <input
                    id="backup-folder"
                    value={draft.folder}
                    placeholder={t("backup.folderPathPlaceholder")}
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
                        <span className="backup-badge ok">{t("backup.connected")}</span>
                        <button type="button" disabled={busy} onClick={() => void disconnect()}>
                          {t("backup.disconnect")}
                        </button>
                      </>
                    ) : config.driveAvailable ? (
                      <a className="backup-connect" href={api.driveAuthURL("backup")}>
                        {t("backup.connectDrive")}
                      </a>
                    ) : null}
                  </div>
                  <div className="notify-field">
                    <label htmlFor="backup-drive-folder">{t("backup.driveFolderOptional")}</label>
                    <div className="backup-drive-folder-row">
                      <input
                        id="backup-drive-folder"
                        value={draft.driveFolderId}
                        placeholder={t("backup.driveFolderPlaceholder")}
                        onChange={(event) => drive.selectFolderId(event.target.value)}
                      />
                      <button
                        type="button"
                        disabled={busy || drive.busy || !connected}
                        onClick={drive.openPicker}
                      >
                        {t("backup.browse")}
                      </button>
                    </div>
                    <span className="backup-drive-folder-current">
                      {t("backup.currentFolder", { label: driveLabel })}
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
                    {t("backup.autoBackup")}
                  </label>
                  {draft.autoBackup && (
                    <div className="notify-field">
                      <label htmlFor="backup-interval">{t("backup.interval")}</label>
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
                        <span>{t("backup.hours")}</span>
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
                        {t("backup.pruneOld")}
                      </label>
                      {draft.pruneOld ? (
                        <>
                          <div className="backup-interval">
                            <input
                              id="backup-retain"
                              aria-label={t("backup.retainedBundles")}
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
                            <span>{t("backup.bundlesUnit")}</span>
                          </div>
                          <p className="backup-hint">
                            {t("backup.retentionHint", {
                              count: draft.retainBundles,
                              hours: draft.intervalHours,
                              coverage,
                              min: MIN_RETAIN,
                            })}
                          </p>
                        </>
                      ) : (
                        <p className="backup-hint">
                          {t("backup.noPruneHint", {
                            count: Math.round((365 * 24) / Math.max(draft.intervalHours, 1)),
                          })}
                        </p>
                      )}
                    </div>
                  )}
                  <p className="backup-hint">
                    {t("backup.lastBackup", { value: lastBackup || t("backup.never") })}
                  </p>
                </div>
              )}

              {backup.canBackup && (
                <div className="backup-bundles">
                  <div className="backup-bundles-head">
                    <label>{t("backup.bundles")}</label>
                    <button type="button" disabled={busy} onClick={backup.reloadBundles}>
                      {t("backup.refresh")}
                    </button>
                  </div>
                  {backup.bundles == null ? (
                    <p className="backup-hint">{t("backup.loading")}</p>
                  ) : backup.bundles.length === 0 ? (
                    <p className="backup-hint">{t("backup.empty")}</p>
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
                            {t("backup.restore")}
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
            <p>{note ? note.text : t("backup.loading")}</p>
          </div>
        )}

        {tab === "backup" && (
          <footer>
            <button
              type="button"
              disabled={pushBlocked}
              onClick={() => void backup.pushWorkspace()}
              title={backup.dirty ? t("backup.saveFirst") : t("backup.workspacePushTitle")}
            >
              {t("backup.workspacePush")}
            </button>
            <button
              type="button"
              disabled={pushBlocked}
              onClick={() => void backup.push()}
              title={backup.dirty ? t("backup.saveFirst") : undefined}
            >
              {t("backup.projectPush")}
            </button>
            <button
              type="button"
              className="primary"
              disabled={busy || !backup.dirty}
              onClick={() => void backup.save()}
            >
              {t("backup.saveSettings")}
            </button>
          </footer>
        )}
      </div>
    </div>
  );
}

function formatLocalizedCoverage(
  count: number,
  hours: number,
  t: (key: string, values?: Record<string, string | number>) => string,
): string {
  const total = count * hours;
  if (!Number.isFinite(total) || total <= 0) return "";
  if (total < 48) return t("backup.coverageHours", { count: Math.round(total) });
  return t("backup.coverageDays", {
    count: Number((total / 24).toFixed(1)),
  });
}

function NoteStrip({ note }: { note: Note }) {
  if (!note) return null;
  return (
    <div className={`notify-message ${note.kind}`} role="status">
      {note.text}
    </div>
  );
}
