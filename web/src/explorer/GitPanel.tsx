import { useEffect } from "react";
import { api } from "../api";
import { explorerApi, type RepoSummary } from "../api/explorer";
import type { Run } from "../api/runs";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { commitSubjectText } from "./commitSubject";
import { clockText, baseName } from "./explorerFormat";
import { useExplorer, type ExplorerView } from "./explorerStore";
import { useLoad } from "./useExplorerData";

/** How many agent branches each repo lists, newest first. */
const MAX_BRANCHES = 20;

function ciChip(status: string | undefined): { key: string; cls: string } | null {
  if (!status) return null;
  if (status === "success") return { key: "explorer.git.ci.pass", cls: "ok" };
  if (status === "failure" || status === "error") return { key: "explorer.git.ci.fail", cls: "bad" };
  return { key: "explorer.git.ci.pending", cls: "" };
}

function verdictChip(verdict: string | undefined): { key: string; cls: string } | null {
  if (verdict === "approve" || verdict === "pass") return { key: "verdict.pass", cls: "ok" };
  if (verdict === "changes" || verdict === "fail") return { key: "verdict.fail", cls: "bad" };
  return null;
}

const same = (a: ExplorerView | null, b: ExplorerView) => JSON.stringify(a) === JSON.stringify(b);

/** The marks the hub gives a pending change, in the order the legend names them. */
const MARKS = ["M", "A", "D", "R"];

/** Names each mark the pending list shows, and only those. */
function marksLegend(pending: { mark: string }[], t: (key: string) => string): string {
  const shown = new Set(pending.map((change) => change.mark));
  return MARKS.filter((mark) => shown.has(mark))
    .map((mark) => t(`explorer.git.mark.${mark}`))
    .join(t("explorer.git.markSeparator"));
}

/** How long a burst of saves settles before the tab asks once. */
const REFRESH_DELAY_MS = 300;

/**
 * Calls reload after the workspace changes under the open tab: a graph save
 * or reload (a new graph rev, which a change from elsewhere also brings), a
 * page save, or the window coming back into focus. A burst becomes one call.
 */
function useWorkspaceChanges(reload: () => void) {
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = () => {
      clearTimeout(timer);
      timer = setTimeout(reload, REFRESH_DELAY_MS);
    };
    const unsubscribe = useApp.subscribe((state, previous) => {
      const saved = state.lastOperation !== previous.lastOperation && state.lastOperation?.status === "saved";
      if (state.graphRev !== previous.graphRev || saved) schedule();
    });
    window.addEventListener("focus", schedule);
    return () => {
      clearTimeout(timer);
      unsubscribe();
      window.removeEventListener("focus", schedule);
    };
  }, [reload]);
}

/** A repo's uncommitted changes, from its runner. */
function RepoChanges({ repo }: { repo: RepoSummary }) {
  const { t } = useI18n();
  const view = useExplorer((s) => s.view);
  const open = useExplorer((s) => s.open);
  const status = useLoad(() => explorerApi.repoStatus(repo.runnerId, repo.repoId), `${repo.runnerId}/${repo.repoId}`);
  const diff: ExplorerView = {
    kind: "diff",
    source: "repo",
    runnerId: repo.runnerId,
    repoId: repo.repoId,
    repoName: repo.name,
    ref: "",
  };
  const label = t("explorer.files.repo", { name: repo.name });
  if (!status.data) return null;
  return (
    <section className="ex-root" aria-label={label}>
      <div className="ex-sec">
        <span className="ex-sec-title">{label}</span>
        {status.data.branch && <span className="ex-chip">{status.data.branch}</span>}
      </div>
      {status.data.entries.length === 0 ? (
        <p className="ex-empty">{t("explorer.git.pendingNone")}</p>
      ) : (
        status.data.entries.map((entry) => (
          <button
            key={entry.path}
            type="button"
            className={`ex-row${same(view, diff) ? " on" : ""}`}
            title={entry.path}
            onClick={() => open(diff)}
          >
            <span className="ex-ico">{entry.mark}</span>
            <span className="ex-name" title={entry.path}>
              {baseName(entry.path)}
            </span>
          </button>
        ))
      )}
    </section>
  );
}

/** Workspace changes, agent branches, commits. */
export default function GitPanel() {
  const { t, language } = useI18n();
  const view = useExplorer((s) => s.view);
  const open = useExplorer((s) => s.open);
  // Both belong to the open project, so a switch from the header loads the
  // new one's, as the Files tab does.
  const project = useApp((s) => s.activeProject);
  const git = useLoad(() => explorerApi.workspaceGit(), `ws-git:${project}`);
  useWorkspaceChanges(git.reload);
  const runs = useLoad(() => api.listProjectRuns(), `runs:${project}`);
  const repos = useLoad(() => explorerApi.repos(), "repos");

  const branches = new Map<string, Run[]>();
  const sorted = [...(runs.data?.runs ?? [])]
    .filter((run) => run.branch && run.kind !== "verify")
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
  for (const run of sorted) {
    const repo = run.repoName || run.repoId || "";
    const list = branches.get(repo) ?? [];
    if (list.length < MAX_BRANCHES && !list.some((other) => other.branch === run.branch)) list.push(run);
    branches.set(repo, list);
  }
  const ws = git.data;
  const branchLabel = ws?.branch || (ws?.backend === "files" ? "" : "main");

  return (
    <div className="ex-pane">
      <div className="ex-phead">
        <h2>{t("explorer.tab.git")}</h2>
      </div>
      <div className="ex-plist">
        {ws && (
          <section className="ex-root" aria-label={t("explorer.git.workspace", { branch: branchLabel })}>
            <div className="ex-sec">
              <span className="ex-sec-title">{t("explorer.git.workspace", { branch: branchLabel })}</span>
            </div>
            {ws.backend === "files" && <p className="ex-note">{t("explorer.git.filesBackend")}</p>}
            {ws.pending.length === 0 ? (
              <p className="ex-empty">{t("explorer.git.pendingNone")}</p>
            ) : (
              ws.pending.map((change) => {
                const target: ExplorerView = { kind: "file", source: "workspace", path: change.path };
                return (
                  <button
                    key={change.path}
                    type="button"
                    className={`ex-row${same(view, target) ? " on" : ""}`}
                    title={change.path}
                    onClick={() => open(target)}
                  >
                    <span className="ex-ico">{change.mark}</span>
                    <span className="ex-name" title={change.path}>
                      {baseName(change.path)}
                    </span>
                  </button>
                );
              })
            )}
            {ws.pending.length > 0 && <p className="ex-hint">{marksLegend(ws.pending, t)}</p>}
          </section>
        )}
        {git.error != null && <p className="ex-note">{(git.error as Error).message ?? String(git.error)}</p>}

        {[...branches.entries()].map(([repo, list]) => (
          <section key={repo} className="ex-root" aria-label={t("explorer.git.branches", { repo })}>
            <div className="ex-sec">
              <span className="ex-sec-title">{t("explorer.git.branches", { repo })}</span>
            </div>
            {list.map((run) => {
              const target: ExplorerView = { kind: "diff", source: "run", runId: run.id };
              const ci = ciChip(run.ciStatus);
              const verdict = verdictChip(run.verdict);
              return (
                <button
                  key={run.id}
                  type="button"
                  className={`ex-row${same(view, target) ? " on" : ""}`}
                  onClick={() => open(target)}
                >
                  <span className="ex-ico" aria-hidden="true">
                    ⎇
                  </span>
                  <span className="ex-name" title={run.branch}>
                    {run.branch}
                  </span>
                  {verdict && <span className={`ex-chip ${verdict.cls}`}>{t(verdict.key)}</span>}
                  {ci && <span className={`ex-chip ${ci.cls}`}>{t(ci.key)}</span>}
                </button>
              );
            })}
          </section>
        ))}

        {(repos.data ?? [])
          .filter((repo) => repo.online && repo.browsable)
          .map((repo) => (
            <RepoChanges key={`${repo.runnerId}/${repo.repoId}`} repo={repo} />
          ))}

        {ws && ws.backend === "git" && (
          <section className="ex-root" aria-label={t("explorer.git.commits")}>
            <div className="ex-sec">
              <span className="ex-sec-title">{t("explorer.git.commits")}</span>
            </div>
            {ws.commits.length === 0 ? (
              <p className="ex-empty">{t("explorer.git.noCommits")}</p>
            ) : (
              ws.commits.map((commit) => {
                const target: ExplorerView = { kind: "diff", source: "workspace", sha: commit.sha };
                return (
                  <button
                    key={commit.sha}
                    type="button"
                    className={`ex-row${same(view, target) ? " on" : ""}`}
                    title={`${commit.sha.slice(0, 8)} · ${commit.author}`}
                    onClick={() => open(target)}
                  >
                    <span className="ex-ico" aria-hidden="true">
                      ○
                    </span>
                    <span className="ex-name" title={commitSubjectText(commit.subject, language)}>
                      {commitSubjectText(commit.subject, language)}
                    </span>
                    <span className="ex-meta">{clockText(commit.at)}</span>
                  </button>
                );
              })
            )}
          </section>
        )}
      </div>
    </div>
  );
}
