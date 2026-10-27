import { useI18n } from "../../i18n";
import { formatWhen } from "./format";
import type { RemoteGitProject } from "./useBackupConfig";

const stateKey: Record<string, string> = {
  synced: "backup.gitStateSynced",
  "local-newer": "backup.gitStateLocalNewer",
  "remote-newer": "backup.gitStateRemoteNewer",
  conflict: "backup.gitStateConflict",
  "remote-missing": "backup.gitStateMissing",
};

/**
 * Each project's branch on the git remote and how its last push went. A
 * branch that moved on the remote is never pushed over, so the row offers
 * the one safe action instead: push this machine's history to a side branch.
 */
export function GitProjectList({
  projects,
  busy,
  onRefresh,
  onKeepLocal,
}: {
  projects: RemoteGitProject[] | null;
  busy: boolean;
  onRefresh: () => void;
  onKeepLocal: (project: string) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="backup-bundles">
      <div className="backup-bundles-head">
        <label>{t("backup.gitProjects")}</label>
        <button type="button" disabled={busy} onClick={onRefresh}>
          {t("backup.refresh")}
        </button>
      </div>
      {projects == null ? (
        <p className="backup-hint">{t("backup.loading")}</p>
      ) : projects.length === 0 ? (
        <p className="backup-hint">{t("backup.gitNoProjects")}</p>
      ) : (
        <ul className="backup-list">
          {projects.map((project) => {
            const diverged = project.state === "conflict" || project.state === "remote-newer";
            const state = project.state ? t(stateKey[project.state] ?? "backup.gitStateUnknown") : "";
            return (
              <li key={project.project}>
                <div className="backup-list-meta">
                  <span className="backup-list-name">
                    {project.project}
                    {project.branch ? ` → ${project.branch}` : ""}
                  </span>
                  <span className="backup-list-sub">
                    {project.error ? `${t("backup.gitStateUnknown")}: ${project.error}` : state}
                  </span>
                  <span className="backup-list-sub">
                    {project.lastPush
                      ? t("backup.gitLastPush", {
                          when: formatWhen(project.lastPush.at),
                          result: project.lastPush.ok
                            ? t("backup.gitPushOk")
                            : t("backup.gitPushFailed", { error: project.lastPush.error ?? "" }),
                        })
                      : t("backup.gitNeverPushed")}
                  </span>
                </div>
                {diverged && (
                  <button
                    type="button"
                    disabled={busy}
                    title={t("backup.gitKeepLocalTitle")}
                    onClick={() => onKeepLocal(project.project)}
                  >
                    {t("backup.gitKeepLocal")}
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
