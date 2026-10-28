import { useMemo, useState, type ReactElement } from "react";
import { explorerApi, type RepoEntry, type RepoSummary, type WorkspaceEntry } from "../api/explorer";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import { baseName } from "./explorerFormat";
import { useExplorer, type ExplorerView } from "./explorerStore";
import { useLoad } from "./useExplorerData";

const INDENT = 14;

interface TreeNode {
  name: string;
  path: string;
  dir: boolean;
  entry?: WorkspaceEntry;
  children: TreeNode[];
}

/** The flat listing as a tree, directories first, then by name. */
function buildTree(entries: WorkspaceEntry[]): TreeNode[] {
  const root: TreeNode = { name: "", path: "", dir: true, children: [] };
  const dirs = new Map<string, TreeNode>([["", root]]);
  const dirFor = (path: string): TreeNode => {
    const found = dirs.get(path);
    if (found) return found;
    const slash = path.lastIndexOf("/");
    const parent = dirFor(slash < 0 ? "" : path.slice(0, slash));
    const node: TreeNode = { name: path.slice(slash + 1), path, dir: true, children: [] };
    parent.children.push(node);
    dirs.set(path, node);
    return node;
  };
  for (const entry of entries) {
    if (entry.type === "dir") {
      dirFor(entry.path);
      continue;
    }
    const slash = entry.path.lastIndexOf("/");
    const parent = dirFor(slash < 0 ? "" : entry.path.slice(0, slash));
    parent.children.push({ name: entry.path.slice(slash + 1), path: entry.path, dir: false, entry, children: [] });
  }
  const sort = (node: TreeNode) => {
    node.children.sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name));
    node.children.forEach(sort);
  };
  sort(root);
  return root.children;
}

function Row({
  icon,
  name,
  meta,
  depth,
  on,
  expanded,
  dim,
  onClick,
}: {
  icon: string;
  name: string;
  meta?: string;
  depth: number;
  on?: boolean;
  expanded?: boolean;
  dim?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      className={`ex-row${on ? " on" : ""}${dim ? " dim" : ""}`}
      style={{ paddingLeft: 8 + depth * INDENT }}
      aria-expanded={expanded}
      disabled={dim}
      onClick={onClick}
    >
      <span className="ex-ico" aria-hidden="true">
        {icon}
      </span>
      <span className="ex-name">{name}</span>
      {meta && <span className="ex-meta">{meta}</span>}
    </button>
  );
}

/** Prose that is not Markdown: notes, logs, a licence. */
const PLAIN_TEXT = /(\.(txt|text|log)|(^|\/)(LICEN[CS]E|COPYING|NOTICE|AUTHORS))$/i;

const fileIcon = (name: string) =>
  /\.(png|jpe?g|gif|webp|svg)$/i.test(name)
    ? "▣"
    : /\.md$/i.test(name)
      ? "¶"
      : PLAIN_TEXT.test(name)
        ? "≡"
        : "{}";

function sameView(a: ExplorerView | null, b: ExplorerView): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** The project's own files, from the hub. */
function WorkspaceFiles({ query }: { query: string }) {
  const { t } = useI18n();
  const project = useApp((s) => s.activeProject);
  const view = useExplorer((s) => s.view);
  const open = useExplorer((s) => s.open);
  const listing = useLoad(() => explorerApi.workspaceFiles(), `ws:${project}`);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [expandedDeep, setExpandedDeep] = useState<Set<string>>(() => new Set());
  const entries = listing.data?.entries ?? [];
  const tree = useMemo(() => buildTree(entries), [entries]);

  // Top-level folders start open, deeper ones closed.
  const isOpen = (node: TreeNode, depth: number) =>
    depth === 0 ? !collapsed.has(node.path) : expandedDeep.has(node.path);
  const toggle = (node: TreeNode, depth: number) => {
    const flip = (set: Set<string>) => {
      const next = new Set(set);
      if (next.has(node.path)) next.delete(node.path);
      else next.add(node.path);
      return next;
    };
    if (depth === 0) setCollapsed(flip);
    else setExpandedDeep(flip);
  };
  const fileView = (path: string): ExplorerView => ({ kind: "file", source: "workspace", path });

  const fileRow = (entry: WorkspaceEntry, depth: number, name = baseName(entry.path), meta = entry.title) => (
    <Row
      key={entry.path}
      icon={fileIcon(entry.path)}
      name={name}
      meta={meta}
      depth={depth}
      on={sameView(view, fileView(entry.path))}
      onClick={() => open(fileView(entry.path))}
    />
  );

  const rows: ReactElement[] = [];
  const q = query.trim().toLowerCase();
  if (q) {
    for (const entry of entries) {
      if (entry.type === "file" && baseName(entry.path).toLowerCase().includes(q)) rows.push(fileRow(entry, 0));
    }
  } else {
    const walk = (nodes: TreeNode[], depth: number) => {
      for (const node of nodes) {
        if (!node.dir) {
          if (node.entry) rows.push(fileRow(node.entry, depth));
          continue;
        }
        const opened = isOpen(node, depth);
        rows.push(
          <Row
            key={`dir:${node.path}`}
            icon={opened ? "▾" : "▸"}
            name={node.name}
            depth={depth}
            expanded={opened}
            onClick={() => toggle(node, depth)}
          />,
        );
        if (opened) walk(node.children, depth + 1);
      }
    };
    walk(tree, 0);
  }

  return (
    <section className="ex-root" aria-label={t("explorer.files.workspace", { project })}>
      <div className="ex-sec">
        <span className="ex-sec-title">{t("explorer.files.workspace", { project })}</span>
        <span className="ex-chip">{t("explorer.files.hub")}</span>
      </div>
      {rows}
      {listing.error != null && <p className="ex-note">{String((listing.error as Error).message ?? listing.error)}</p>}
      {listing.data?.truncated && <p className="ex-note">{t("explorer.files.truncated", { count: entries.length })}</p>}
    </section>
  );
}

/** One directory of a repo, fetched from its runner when opened. */
function RepoDir({ repo, path, depth, query }: { repo: RepoSummary; path: string; depth: number; query: string }) {
  const { t } = useI18n();
  const view = useExplorer((s) => s.view);
  const open = useExplorer((s) => s.open);
  const listing = useLoad(
    () => explorerApi.repoTree(repo.runnerId, repo.repoId, path),
    `${repo.runnerId}/${repo.repoId}/${path}`,
  );
  const [opened, setOpened] = useState<Set<string>>(() => new Set());
  const q = query.trim().toLowerCase();
  const join = (name: string) => (path ? `${path}/${name}` : name);
  const fileView = (entryPath: string): ExplorerView => ({
    kind: "file",
    source: "repo",
    runnerId: repo.runnerId,
    runnerName: repo.runnerName,
    repoId: repo.repoId,
    path: entryPath,
  });

  const entries = (listing.data?.entries ?? []).filter(
    (entry: RepoEntry) => !q || entry.type === "dir" || entry.name.toLowerCase().includes(q),
  );
  return (
    <>
      {entries.map((entry) => {
        const entryPath = join(entry.name);
        if (entry.type === "dir") {
          const isOpen = opened.has(entryPath);
          return (
            <div key={`dir:${entryPath}`}>
              <Row
                icon={isOpen ? "▾" : "▸"}
                name={entry.name}
                depth={depth}
                expanded={isOpen}
                onClick={() =>
                  setOpened((set) => {
                    const next = new Set(set);
                    if (next.has(entryPath)) next.delete(entryPath);
                    else next.add(entryPath);
                    return next;
                  })
                }
              />
              {isOpen && <RepoDir repo={repo} path={entryPath} depth={depth + 1} query={query} />}
            </div>
          );
        }
        if (entry.escape) {
          return (
            <Row key={entryPath} icon="↗" name={entry.name} meta={t("explorer.files.escape")} depth={depth} dim />
          );
        }
        return (
          <Row
            key={entryPath}
            icon={fileIcon(entry.name)}
            name={entry.name}
            depth={depth}
            on={sameView(view, fileView(entryPath))}
            onClick={() => open(fileView(entryPath))}
          />
        );
      })}
      {depth === 0 && listing.data && (
        <p className="ex-note">
          {listing.data.hidden > 0
            ? t("explorer.files.readOnlyNote", { count: listing.data.hidden })
            : t("explorer.files.readOnlyNoteNone")}
        </p>
      )}
      {listing.data?.truncated && (
        <p className="ex-note">{t("explorer.files.truncated", { count: listing.data.entries.length })}</p>
      )}
      {listing.error != null && <p className="ex-note">{String((listing.error as Error).message ?? listing.error)}</p>}
    </>
  );
}

function RepoSection({ repo, query }: { repo: RepoSummary; query: string }) {
  const { t } = useI18n();
  const label = t("explorer.files.repo", { name: repo.name });
  let body: ReactElement;
  if (!repo.online) {
    body = <p className="ex-note">{t("explorer.files.offlineNote", { runner: repo.runnerName })}</p>;
  } else if (!repo.browsable) {
    body = (
      <p className="ex-note">
        {repo.reason && repo.reason !== "REPO_BROWSE_OFF" ? repo.reason : t("explorer.files.browseOff")}
      </p>
    );
  } else {
    body = <RepoDir repo={repo} path="" depth={0} query={query} />;
  }
  return (
    <section className="ex-root" aria-label={label}>
      <div className="ex-sec">
        <span className="ex-sec-title">{label}</span>
        <span className={`ex-chip${repo.online ? "" : " bad"}`}>
          {repo.online ? repo.runnerName : t("explorer.files.offline")}
        </span>
      </div>
      {body}
    </section>
  );
}

/** Workspace files and the runners' repos, read-only. */
export default function FilesPanel() {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const repos = useLoad(() => explorerApi.repos(), "repos");
  return (
    <div className="ex-pane">
      <div className="ex-phead">
        <h2>{t("explorer.tab.files")}</h2>
      </div>
      <input
        className="ex-filter"
        type="search"
        placeholder={t("explorer.files.search")}
        aria-label={t("explorer.files.search")}
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
      <div className="ex-plist">
        <WorkspaceFiles query={query} />
        {(repos.data ?? []).map((repo) => (
          <RepoSection key={`${repo.runnerId}/${repo.repoId}`} repo={repo} query={query} />
        ))}
        {repos.data && repos.data.length === 0 && <p className="ex-note">{t("explorer.files.noRepos")}</p>}
      </div>
    </div>
  );
}
