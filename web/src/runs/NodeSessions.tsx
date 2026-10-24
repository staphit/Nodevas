import { useCallback, useEffect, useState } from "react";
import type { SessionChange } from "../api";
import { IconTerminal } from "../icons";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { relativeTime } from "../runners/RunnersModal";
import { DiffDialog } from "./DiffViewer";
import { filesLabel, LineCounts } from "./RunChanges";
import { formatCount } from "./runFormat";
import { useSessions } from "./sessionStore";

/** "nodevas@feat/x", "nodevas", "@feat/x" or "". */
export function repoAtBranch(change: Pick<SessionChange, "repo" | "branch">): string {
  const repo = change.repo ?? "";
  const branch = change.branch ?? "";
  if (repo && branch) return `${repo}@${branch}`;
  return repo || (branch ? `@${branch}` : "");
}

function sessionLabel(change: SessionChange): string {
  return change.session || change.sessionId || change.agent;
}

/** A snapshot the diff viewer can show: one with a file or a patch in it. */
function hasSnapshot(change: SessionChange): boolean {
  const snapshot = change.snapshot;
  return Boolean(snapshot && ((snapshot.files?.length ?? 0) > 0 || snapshot.patch || snapshot.patchTruncated));
}

function SessionRow({ change, onViewDiff }: { change: SessionChange; onViewDiff: () => void }) {
  const { t, language } = useI18n();
  const files = change.files ?? [];
  const where = repoAtBranch(change);
  const snapshot = change.snapshot;
  return (
    <li className="session-item" data-testid={`session-${change.id}`}>
      <div className="session-top">
        <strong className="session-name">{sessionLabel(change)}</strong>
        {change.agent && <span className="session-agent">{change.agent}</span>}
        <time
          dateTime={change.updatedAt}
          title={formatLocalizedDateTime(change.updatedAt, language)}
        >
          {relativeTime(change.updatedAt)}
        </time>
      </div>
      {(where || snapshot) && (
        <div className="run-row-meta">
          {where && <span className="mono">{where}</span>}
          {snapshot && (
            <span>
              <LineCounts additions={snapshot.additions} deletions={snapshot.deletions} />
            </span>
          )}
        </div>
      )}
      {files.length > 0 ? (
        <details className="session-files">
          <summary>{t("sessions.filesTouched", { count: formatCount(files.length) })}</summary>
          <ul>
            {files.map((path) => (
              <li key={path} className="mono" title={path}>
                {path}
              </li>
            ))}
          </ul>
        </details>
      ) : (
        <span className="session-none">{t("sessions.noFiles")}</span>
      )}
      {hasSnapshot(change) && (
        <button type="button" className="run-link-button session-diff" onClick={onViewDiff}>
          {t("sessions.viewDiff")}
        </button>
      )}
    </li>
  );
}

/** A session's diff snapshot, taken when it stopped, in the run diff viewer. */
export function SessionDiffDialog({ change, onClose }: { change: SessionChange; onClose: () => void }) {
  const { t } = useI18n();
  const where = repoAtBranch(change);
  return (
    <DiffDialog
      changes={change.snapshot ?? null}
      title={t("sessions.diffTitle")}
      subtitle={[sessionLabel(change), change.agent !== sessionLabel(change) ? change.agent : "", where]
        .filter(Boolean)
        .join(" · ")}
      summary={(snapshot, count) => (
        <div className="run-changes">
          <span className="run-changes-line">
            <LineCounts additions={snapshot.additions} deletions={snapshot.deletions} />
            <span className="run-changes-files">{filesLabel(t, count)}</span>
            <span className="session-snapshot-note">{t("sessions.snapshotNote")}</span>
          </span>
        </div>
      )}
      onClose={onClose}
    />
  );
}

/**
 * Agents run in the owner's own terminal that edited this node (W6): who,
 * where, which files, and the diff they left at Stop. Shown under the node's
 * runs; nothing at all when there are none.
 */
export function NodeSessions({ id }: { id: string }) {
  const { t } = useI18n();
  const list = useSessions((s) => s.byNode[id]);
  const error = useSessions((s) => s.errors[id]);
  const load = useSessions((s) => s.load);
  const [openId, setOpenId] = useState<string | null>(null);
  const close = useCallback(() => setOpenId(null), []);

  useEffect(() => {
    void load(id);
  }, [id, load]);

  const changes = list ?? [];
  const open = changes.find((change) => change.id === openId);
  if (changes.length === 0 && !error) return null;

  return (
    <section className="node-sessions" aria-labelledby={`node-sessions-${id}`}>
      <div className="node-runs-head">
        <h3 id={`node-sessions-${id}`}>
          <IconTerminal size={14} />
          {t("sessions.title")}
          {changes.length > 0 && <span className="node-runs-count">{changes.length}</span>}
        </h3>
      </div>
      <p className="run-hint">{t("sessions.hint")}</p>
      {error && (
        <div className="notify-message error" role="status">
          {error}
        </div>
      )}
      {changes.length > 0 && (
        <ul className="session-list" aria-label={t("sessions.title")}>
          {changes.map((change) => (
            <SessionRow key={change.id} change={change} onViewDiff={() => setOpenId(change.id)} />
          ))}
        </ul>
      )}
      {open && <SessionDiffDialog change={open} onClose={close} />}
    </section>
  );
}
