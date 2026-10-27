import { send } from "./http";

/**
 * The Explorer's data (internal/httpapi/explorer): the workspace's own files
 * and history, read-only browsing of the repos registered on the viewer's own
 * runners (the runner reads them; the hub only relays), and the agents at
 * work. Every route is a person's: an agent is refused.
 */

export interface WorkspaceEntry {
  path: string;
  type: "file" | "dir";
  size: number;
  nodeId?: string;
  title?: string;
}

export interface WorkspaceListing {
  entries: WorkspaceEntry[];
  hidden: number;
  truncated: boolean;
}

export interface WorkspaceFile {
  path: string;
  size: number;
  kind: "text" | "image" | "binary";
  content?: string;
  truncated: boolean;
  nodeId?: string;
  url?: string;
}

export interface PendingChange {
  path: string;
  mark: string;
}

export interface CommitInfo {
  sha: string;
  at: string;
  author: string;
  subject: string;
}

export interface WorkspaceGit {
  backend: "git" | "files";
  branch: string;
  pending: PendingChange[];
  commits: CommitInfo[];
}

export interface CommitPatch extends CommitInfo {
  patch: string;
  truncated: boolean;
}

export interface RepoSummary {
  runnerId: string;
  runnerName: string;
  online: boolean;
  repoId: string;
  name: string;
  browsable: boolean;
  reason?: string;
}

export interface RepoEntry {
  name: string;
  type: "file" | "dir" | "symlink";
  size: number;
  escape?: boolean;
}

export interface RepoListing {
  path: string;
  entries: RepoEntry[];
  hidden: number;
  truncated: boolean;
}

export interface RepoFile {
  path: string;
  size: number;
  binary: boolean;
  truncated: boolean;
  content: string;
}

export interface RepoGitStatus {
  branch: string;
  head: string;
  entries: PendingChange[];
  hidden: number;
  truncated: boolean;
}

export interface RepoDiff {
  ref: string;
  patch: string;
  truncated: boolean;
  hidden: number;
}

export interface AgentTokens {
  input: number;
  output: number;
  cacheRead: number;
}

export interface AgentVerdict {
  outcome: "pass" | "fail";
  severity: string;
  reasons: string[];
}

export interface AgentItem {
  kind: "run" | "session";
  id: string;
  runId?: string;
  sessionId?: string;
  agent: string;
  model: string;
  /** Only ever the run's recorded role; an external session has none. */
  role?: string;
  state: string;
  nodeId: string;
  nodeTitle: string;
  tokens: AgentTokens;
  costUsd: number;
  costEstimated: boolean;
  verdict?: AgentVerdict;
  startedAt: string;
  finishedAt: string;
  runnerName?: string;
  testState?: "red" | "green";
  files: PendingChange[];
}

export interface AgentsResponse {
  live: AgentItem[];
  external: AgentItem[];
  recent: AgentItem[];
}

export type SessionActionKind = "joined" | "left" | "doing" | "message" | "ask" | "changes" | "usage";

export interface SessionAction {
  at: string;
  action: SessionActionKind | string;
  nodeId: string;
  detail: string;
}

/** The codes the Explorer reacts to. */
export type ExplorerErrorCode =
  | "RUNNER_OFFLINE"
  | "REPO_BROWSE_OFF"
  | "HIDDEN_FILE"
  | "BAD_PATH"
  | "NOT_FOUND"
  | "TIMEOUT"
  | "";

/** A refused Explorer request with the server's code. */
export class ExplorerError extends Error {
  status: number;
  code: ExplorerErrorCode;
  constructor(status: number, code: ExplorerErrorCode, message: string) {
    super(message);
    this.name = "ExplorerError";
    this.status = status;
    this.code = code;
  }
}

/** The URL of one repo route: runner and repo ids path-escaped. */
export function repoUrl(runnerId: string, repoId: string, rest: string): string {
  return `/api/explorer/repos/${encodeURIComponent(runnerId)}/${encodeURIComponent(repoId)}/${rest}`;
}

const CODES: readonly ExplorerErrorCode[] = [
  "RUNNER_OFFLINE",
  "REPO_BROWSE_OFF",
  "HIDDEN_FILE",
  "BAD_PATH",
  "NOT_FOUND",
  "TIMEOUT",
];

/** GET url; a refusal becomes an ExplorerError carrying the server's code. */
async function get<T>(url: string): Promise<T> {
  const res = await send(url);
  if (!res.ok) {
    const body = (await res.json().catch(() => ({}))) as { error?: unknown; code?: unknown };
    const raw = typeof body.code === "string" ? body.code : "";
    const code = (CODES as readonly string[]).includes(raw) ? (raw as ExplorerErrorCode) : "";
    const message = typeof body.error === "string" && body.error ? body.error : `${res.status}`;
    throw new ExplorerError(res.status, code, message);
  }
  return (await res.json()) as T;
}

const q = (value: string) => encodeURIComponent(value);
const arr = <T>(value: T[] | null | undefined): T[] => value ?? [];

export const explorerApi = {
  workspaceFiles: (): Promise<WorkspaceListing> =>
    get<WorkspaceListing>("/api/explorer/workspace/files").then((r) => ({ ...r, entries: arr(r.entries) })),
  workspaceFile: (path: string): Promise<WorkspaceFile> =>
    get<WorkspaceFile>(`/api/explorer/workspace/file?path=${q(path)}`),
  workspaceGit: (): Promise<WorkspaceGit> =>
    get<WorkspaceGit>("/api/explorer/workspace/git").then((r) => ({
      ...r,
      pending: arr(r.pending),
      commits: arr(r.commits),
    })),
  workspaceCommit: (sha: string): Promise<CommitPatch> =>
    get<CommitPatch>(`/api/explorer/workspace/git/commit?sha=${q(sha)}`),
  repos: (): Promise<RepoSummary[]> =>
    get<{ repos: RepoSummary[] | null }>("/api/explorer/repos").then((r) => arr(r.repos)),
  repoTree: (runnerId: string, repoId: string, path: string): Promise<RepoListing> =>
    get<RepoListing>(repoUrl(runnerId, repoId, `tree?path=${q(path)}`)).then((r) => ({ ...r, entries: arr(r.entries) })),
  repoFile: (runnerId: string, repoId: string, path: string): Promise<RepoFile> =>
    get<RepoFile>(repoUrl(runnerId, repoId, `file?path=${q(path)}`)),
  repoStatus: (runnerId: string, repoId: string): Promise<RepoGitStatus> =>
    get<RepoGitStatus>(repoUrl(runnerId, repoId, "git/status")).then((r) => ({ ...r, entries: arr(r.entries) })),
  repoLog: (runnerId: string, repoId: string, limit = 30): Promise<CommitInfo[]> =>
    get<{ commits: CommitInfo[] | null }>(repoUrl(runnerId, repoId, `git/log?limit=${limit}`)).then((r) =>
      arr(r.commits),
    ),
  repoDiff: (runnerId: string, repoId: string, ref: string): Promise<RepoDiff> =>
    get<RepoDiff>(repoUrl(runnerId, repoId, `git/diff?ref=${q(ref)}`)),
  agents: (): Promise<AgentsResponse> =>
    get<AgentsResponse>("/api/explorer/agents").then((r) => ({
      live: arr(r.live),
      external: arr(r.external),
      recent: arr(r.recent),
    })),
  sessionActions: (sessionId: string, limit = 200): Promise<SessionAction[]> =>
    get<{ actions: SessionAction[] | null }>(`/api/explorer/sessions/${q(sessionId)}/actions?limit=${limit}`).then((r) =>
      arr(r.actions),
    ),
};
