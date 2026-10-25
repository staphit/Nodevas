import { useEffect } from "react";
import { useI18n } from "../i18n";
import { IconClose, IconCloud } from "../icons";
import { formatSize, formatWhen } from "./backup/format";
import {
  MIN_RETAIN,
  useBackupConfig,
  type Note,
} from "./backup/useBackupConfig";

/**
 * Backup to a folder on the server, and restore from it.
 *
 * The server is always the single writer; backup and restore are never on the
 * write path.
 */
export function BackupModal({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const backup = useBackupConfig();
  const { busy, config, draft, patch, note } = backup;

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onClose]);

  const lastBackup =
    config && !config.lastBackupAt.startsWith("0001-01-01")
      ? formatWhen(config.lastBackupAt)
      : "";
  const coverage = formatLocalizedCoverage(draft.retainBundles, draft.intervalHours, t);
  // Pushing while the form disagrees with the server would back up settings
  // nobody chose, so both buttons wait for the saved settings.
  const pushBlocked = busy || !backup.canBackup || backup.dirty;

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

        {config ? (
          <div className="notify-form">
            <label className="notify-toggle">
              <input
                type="checkbox"
                checked={draft.kind === "folder"}
                onChange={(event) => patch({ kind: event.target.checked ? "folder" : "" })}
              />
              {t("backup.enableFolder")}
            </label>

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
          <div className="notify-form">
            <p>{note ? note.text : t("backup.loading")}</p>
          </div>
        )}

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
