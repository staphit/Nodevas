import { useEffect } from "react";
import { DialogFrame, useInlineDialog } from "./DialogFrame";
import { useI18n } from "../i18n";
import { IconClose, IconCloud } from "../icons";
import { formatSize, formatWhen } from "./backup/format";
import { GitProjectList } from "./backup/GitProjectList";
import { MIN_RETAIN, useBackupConfig, type Note } from "./backup/useBackupConfig";

/**
 * Backup to a folder on the server, and restore from it; or to a git remote,
 * which gets each project's own history as a branch.
 *
 * The server is always the single writer; backup and restore are never on the
 * write path.
 */
export function BackupModal({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const inline = useInlineDialog();
  const backup = useBackupConfig();
  const { busy, config, draft, patch, note } = backup;

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy && !inline) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, onClose, inline]);

  const lastBackup = config && !config.lastBackupAt.startsWith("0001-01-01") ? formatWhen(config.lastBackupAt) : "";
  const coverage = formatLocalizedCoverage(draft.retainBundles, draft.intervalHours, t);
  // Pushing while the form disagrees with the server would back up settings
  // nobody chose, so both buttons wait for the saved settings.
  const pushBlocked = busy || !backup.canBackup || backup.dirty;
  // Retention and the bundle list are the folder backend's: a git remote keeps
  // every commit and holds branches, not bundles.
  const isFolder = config?.kind !== "git" && draft.kind !== "git";

  return (
    <DialogFrame className="notify-dialog" label={t("backup.title")} onDismiss={() => !busy && onClose()}>
      <header>
        <span className="confirm-dialog-icon">
          <IconCloud size={17} />
        </span>
        <div>
          <h2>{t("backup.title")}</h2>
          <p>{t("backup.description")}</p>
        </div>
        <button
          type="button"
          className="confirm-dialog-close"
          data-dialog-close
          onClick={onClose}
          aria-label={t("backup.close")}
          disabled={busy}
        >
          <IconClose size={14} />
        </button>
      </header>

      {config ? (
        <div className="notify-form">
          <div role="radiogroup" aria-label={t("backup.destination")}>
            {(
              [
                ["", "backup.kindOff"],
                ["folder", "backup.enableFolder"],
                ["git", "backup.enableGit"],
              ] as const
            ).map(([kind, label]) => (
              <label className="notify-toggle" key={kind || "off"}>
                <input type="radio" name="backup-kind" checked={draft.kind === kind} onChange={() => patch({ kind })} />
                {t(label)}
              </label>
            ))}
          </div>

          {draft.kind === "git" && (
            <>
              <div className="notify-field">
                <label htmlFor="backup-git-url">{t("backup.gitUrl")}</label>
                <input
                  id="backup-git-url"
                  type="url"
                  value={draft.gitUrl}
                  placeholder={t("backup.gitUrlPlaceholder")}
                  onChange={(event) => patch({ gitUrl: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="backup-git-user">{t("backup.gitUser")}</label>
                <input
                  id="backup-git-user"
                  value={draft.gitUser}
                  placeholder="x-access-token"
                  autoComplete="off"
                  onChange={(event) => patch({ gitUser: event.target.value })}
                />
              </div>
              <div className="notify-field">
                <label htmlFor="backup-git-token">{t("backup.gitToken")}</label>
                <input
                  id="backup-git-token"
                  type="password"
                  value={draft.gitToken}
                  autoComplete="new-password"
                  placeholder={config.hasGitToken && !draft.clearGitToken ? t("backup.gitTokenSaved") : ""}
                  onChange={(event) => patch({ gitToken: event.target.value })}
                />
                {config.hasGitToken && !draft.gitToken && (
                  <label className="notify-toggle">
                    <input
                      type="checkbox"
                      checked={draft.clearGitToken}
                      onChange={(event) => patch({ clearGitToken: event.target.checked })}
                    />
                    {t("backup.gitForgetToken")}
                  </label>
                )}
              </div>
              <p className="backup-hint">{t("backup.gitHint")}</p>
              <div>
                <button
                  type="button"
                  disabled={busy || !draft.gitUrl.trim()}
                  onClick={() => void backup.testConnection()}
                >
                  {t("backup.gitTest")}
                </button>
              </div>
            </>
          )}

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
              {draft.autoBackup && isFolder && (
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
              <p className="backup-hint">{t("backup.lastBackup", { value: lastBackup || t("backup.never") })}</p>
            </div>
          )}

          {backup.canBackup && config.kind === "git" && draft.kind === "git" && (
            <GitProjectList
              projects={backup.gitProjects}
              busy={busy}
              onRefresh={backup.reloadGitProjects}
              onKeepLocal={(project) => void backup.keepLocal(project)}
            />
          )}

          {backup.canBackup && isFolder && (
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
                      <button type="button" disabled={busy} onClick={() => void backup.restore(bundle)}>
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
        <button type="button" className="primary" disabled={busy || !backup.dirty} onClick={() => void backup.save()}>
          {t("backup.saveSettings")}
        </button>
      </footer>
    </DialogFrame>
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
