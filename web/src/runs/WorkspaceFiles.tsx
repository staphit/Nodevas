import { useState, type ReactNode } from "react";
import { api, forgetRunWorkspace, type Run, type RunWorkspaceView } from "../api";
import { ExplorerError, type RepoEntry, type RepoListing } from "../api/explorer";
import { baseName, sizeText } from "../explorer/explorerFormat";
import { CodeLines } from "../explorer/FileView";
import { useLoad, type Loaded } from "../explorer/useExplorerData";
import { IconRefresh } from "../icons";
import { useI18n } from "../i18n";
import { runnerDisplayName } from "../runners/runnerName";

const INDENT = 14;
/** The cap the hub and the runner put on a file's text. */
const TEXT_CAP = 512 << 10;

/** Why the Files tab shows nothing, as the hub or a refused read says it. */
const FILE_BLOCKS: Record<string, string> = {
  RUNNER_OFFLINE: "offline",
  REPO_BROWSE_OFF: "browse_off",
  RUNNER_OUTDATED: "outdated",
  NO_WORKTREE: "no_worktree",
};

function errorText(t: ReturnType<typeof useI18n>["t"], error: unknown, runner: string): string {
  if (error instanceof ExplorerError) {
    const block = FILE_BLOCKS[error.code];
    if (block) return t(`runWorkspace.files.${block}`, { runner });
    if (error.code === "HIDDEN_FILE") return t("explorer.file.hidden");
  }
  return error instanceof Error ? error.message : String(error);
}

/** Whether a refusal means the whole worktree is out of reach, not one file. */
function blocksAll(error: unknown): boolean {
  return error instanceof ExplorerError && error.code in FILE_BLOCKS;
}

interface DirProps {
  runId: string;
  path: string;
  depth: number;
  /** Bumped by Refresh: the worktree is live, so every folder asks again. */
  nonce: number;
  runner: string;
  selected: string | null;
  onOpen: (path: string) => void;
}

/** One folder of the run's worktree, fetched from its runner when opened. */
function WorkspaceDir(props: DirProps) {
  const { runId, path, nonce } = props;
  const listing = useLoad(() => api.workspaceTree(runId, path), `${runId}:${nonce}:${path}`);
  return <DirRows {...props} listing={listing} />;
}

/** A folder's entries: folders first, then files, links out dimmed. */
function DirRows({
  runId,
  path,
  depth,
  nonce,
  runner,
  selected,
  onOpen,
  listing,
}: DirProps & { listing: Loaded<RepoListing> }) {
  const { t } = useI18n();
  const [opened, setOpened] = useState<Set<string>>(() => new Set());
  const join = (name: string) => (path ? `${path}/${name}` : name);
  // Folders first, then by name, as the Explorer lists a repo.
  const entries = [...(listing.data?.entries ?? [])].sort(
    (a: RepoEntry, b: RepoEntry) => Number(b.type === "dir") - Number(a.type === "dir") || a.name.localeCompare(b.name),
  );
  const pad = { paddingLeft: 8 + depth * INDENT };
  return (
    <>
      {entries.map((entry) => {
        const entryPath = join(entry.name);
        if (entry.type === "dir") {
          const isOpen = opened.has(entryPath);
          return (
            <li key={`dir:${entryPath}`} className="run-ws-tree-item">
              <button
                type="button"
                className="run-ws-row"
                style={pad}
                aria-expanded={isOpen}
                onClick={() =>
                  setOpened((set) => {
                    const next = new Set(set);
                    if (next.has(entryPath)) next.delete(entryPath);
                    else next.add(entryPath);
                    return next;
                  })
                }
              >
                <span className="run-ws-ico" aria-hidden="true">
                  {isOpen ? "▾" : "▸"}
                </span>
                <span className="run-ws-name">{entry.name}</span>
              </button>
              {isOpen && (
                <ul className="run-ws-tree" role="group">
                  <WorkspaceDir
                    runId={runId}
                    path={entryPath}
                    depth={depth + 1}
                    nonce={nonce}
                    runner={runner}
                    selected={selected}
                    onOpen={onOpen}
                  />
                </ul>
              )}
            </li>
          );
        }
        if (entry.type === "other") {
          // A pipe, socket or device the agent left: named, never read.
          return (
            <li key={entryPath} className="run-ws-tree-item">
              <button type="button" className="run-ws-row dim" style={pad} disabled title={t("runWorkspace.files.special")}>
                <span className="run-ws-ico" aria-hidden="true">
                  ?
                </span>
                <span className="run-ws-name">{entry.name}</span>
              </button>
            </li>
          );
        }
        if (entry.escape) {
          // A link out of the worktree: named, never followed.
          return (
            <li key={entryPath} className="run-ws-tree-item">
              <button type="button" className="run-ws-row dim" style={pad} disabled title={t("explorer.files.escape")}>
                <span className="run-ws-ico" aria-hidden="true">
                  ↗
                </span>
                <span className="run-ws-name">{entry.name}</span>
                <span className="run-ws-meta">{t("explorer.files.escape")}</span>
              </button>
            </li>
          );
        }
        return (
          <li key={entryPath} className="run-ws-tree-item">
            <button
              type="button"
              className={`run-ws-row${selected === entryPath ? " on" : ""}`}
              style={pad}
              aria-current={selected === entryPath ? "true" : undefined}
              onClick={() => onOpen(entryPath)}
            >
              <span className="run-ws-ico" aria-hidden="true">
                {entry.type === "symlink" ? "↪" : "·"}
              </span>
              <span className="run-ws-name">{entry.name}</span>
            </button>
          </li>
        );
      })}
      {depth === 0 && listing.data && (
        <li className="run-ws-note">
          {listing.data.hidden > 0
            ? t("explorer.files.readOnlyNote", { count: listing.data.hidden })
            : t("explorer.files.readOnlyNoteNone")}
        </li>
      )}
      {listing.data?.truncated && (
        <li className="run-ws-note">{t("explorer.files.truncated", { count: listing.data.entries.length })}</li>
      )}
      {listing.error != null && (
        <li className="run-ws-note" role={depth === 0 ? "alert" : undefined}>
          {errorText(t, listing.error, runner)}
        </li>
      )}
      {listing.loading && !listing.data && listing.error == null && (
        <li className="run-ws-note">{t("common.loading")}</li>
      )}
    </>
  );
}

/** One file of the worktree, read-only, with line numbers. */
function WorkspaceFileView({
  runId,
  path,
  nonce,
  runner,
}: {
  runId: string;
  path: string;
  nonce: number;
  runner: string;
}) {
  const { t } = useI18n();
  const file = useLoad(() => api.workspaceFile(runId, path), `${runId}:${nonce}:${path}`);
  let body: ReactNode = null;
  if (file.error != null) {
    body = <p className="run-ws-note">{errorText(t, file.error, runner)}</p>;
  } else if (file.data) {
    if (file.data.binary) {
      body = <p className="run-ws-note">{t("explorer.file.binary")}</p>;
    } else {
      body = (
        <>
          {file.data.truncated && (
            <p className="run-ws-note">{t("explorer.file.truncatedFile", { size: sizeText(TEXT_CAP) })}</p>
          )}
          <CodeLines text={file.data.content} />
        </>
      );
    }
  } else {
    body = <p className="run-ws-note">{t("common.loading")}</p>;
  }
  return (
    <section className="run-ws-file" aria-label={path}>
      <div className="run-ws-file-head">
        <strong>{baseName(path)}</strong>
        <span className="mono">{path}</span>
      </div>
      <div className="run-ws-file-body">{body}</div>
    </section>
  );
}

/** What is uncommitted in the worktree, against its HEAD. */
function Uncommitted({
  runId,
  nonce,
  runner,
  onOpen,
}: {
  runId: string;
  nonce: number;
  runner: string;
  onOpen: (path: string) => void;
}) {
  const { t } = useI18n();
  const status = useLoad(() => api.workspaceStatus(runId), `${runId}:${nonce}:status`);
  if (status.error != null) return <p className="run-ws-note">{errorText(t, status.error, runner)}</p>;
  if (!status.data) return <p className="run-ws-note">{t("common.loading")}</p>;
  if (status.data.entries.length === 0) return <p className="run-ws-note">{t("runWorkspace.files.noUncommitted")}</p>;
  return (
    <ul className="run-ws-status" aria-label={t("runWorkspace.files.uncommitted")}>
      {status.data.entries.map((entry) => (
        <li key={entry.path}>
          <button type="button" className="run-ws-row" onClick={() => onOpen(entry.path)}>
            <span className="run-ws-mark mono">{entry.mark}</span>
            <span className="run-ws-name mono">{entry.path}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

/**
 * The Files tab: the run's live worktree, read-only, through its runner. The
 * runner scopes every read to that one worktree (no path out of it, no link
 * followed out of it, secrets and .git hidden). Only the runner's owner may
 * look; anyone else, or a runner that is away, gets one note instead.
 */
export function WorkspaceFiles({ run, view }: { run: Run; view: RunWorkspaceView | undefined }) {
  const { t } = useI18n();
  const runner = runnerDisplayName(t, run.runnerName) || run.runnerId;
  if (!view) return <p className="run-ws-note">{t("common.loading")}</p>;
  if (!view.files.allowed) {
    const block = view.files.blocked || "not_owner";
    return <p className="run-ws-note run-ws-files-blocked">{t(`runWorkspace.files.${block}`, { runner })}</p>;
  }
  return <WorkspaceBrowser run={run} runner={runner} />;
}

function WorkspaceBrowser({ run, runner }: { run: Run; runner: string }) {
  const { t } = useI18n();
  const [selected, setSelected] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);
  const [showStatus, setShowStatus] = useState(false);
  // The first folder's refusal (the worktree is gone, the runner went away)
  // speaks for the whole tab.
  const root = useLoad(() => api.workspaceTree(run.id, ""), `${run.id}:${nonce}:`);

  if (root.error != null && blocksAll(root.error)) {
    return <p className="run-ws-note run-ws-files-blocked">{errorText(t, root.error, runner)}</p>;
  }

  return (
    <div className="run-ws-files">
      <div className="run-ws-files-bar">
        <button
          type="button"
          className="run-ws-tool"
          onClick={() => {
            forgetRunWorkspace(run.id);
            setNonce((n) => n + 1);
          }}
        >
          <IconRefresh size={13} />
          <span>{t("runWorkspace.files.refresh")}</span>
        </button>
        <button
          type="button"
          className="run-ws-tool"
          aria-pressed={showStatus}
          onClick={() => setShowStatus((on) => !on)}
        >
          {t("runWorkspace.files.uncommitted")}
        </button>
      </div>
      <div className="run-ws-files-body">
        <nav className="run-ws-files-list" aria-label={t("runWorkspace.files.tree")}>
          {showStatus ? (
            <Uncommitted runId={run.id} nonce={nonce} runner={runner} onOpen={setSelected} />
          ) : (
            <ul className="run-ws-tree">
              <DirRows
                runId={run.id}
                path=""
                depth={0}
                nonce={nonce}
                runner={runner}
                selected={selected}
                onOpen={setSelected}
                listing={root}
              />
            </ul>
          )}
        </nav>
        {selected ? (
          <WorkspaceFileView runId={run.id} path={selected} nonce={nonce} runner={runner} />
        ) : (
          <p className="run-ws-note run-ws-pick">{t("runWorkspace.files.pick")}</p>
        )}
      </div>
    </div>
  );
}
