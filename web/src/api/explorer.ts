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
export function repoUrl(_runnerId: string, _repoId: string, _rest: string): string {
  // TODO(green): implement.
  return "";
}

function notImplemented(): Promise<never> {
  return Promise.reject(new Error("explorer API: not implemented"));
}

export const explorerApi = {
  workspaceFiles: (): Promise<WorkspaceListing> => notImplemented(),
  workspaceFile: (_path: string): Promise<WorkspaceFile> => notImplemented(),
  workspaceGit: (): Promise<WorkspaceGit> => notImplemented(),
  workspaceCommit: (_sha: string): Promise<CommitPatch> => notImplemented(),
  repos: (): Promise<RepoSummary[]> => notImplemented(),
  repoTree: (_runnerId: string, _repoId: string, _path: string): Promise<RepoListing> => notImplemented(),
  repoFile: (_runnerId: string, _repoId: string, _path: string): Promise<RepoFile> => notImplemented(),
  repoStatus: (_runnerId: string, _repoId: string): Promise<RepoGitStatus> => notImplemented(),
  repoLog: (_runnerId: string, _repoId: string, _limit?: number): Promise<CommitInfo[]> => notImplemented(),
  repoDiff: (_runnerId: string, _repoId: string, _ref: string): Promise<RepoDiff> => notImplemented(),
  agents: (): Promise<AgentsResponse> => notImplemented(),
  sessionActions: (_sessionId: string, _limit?: number): Promise<SessionAction[]> => notImplemented(),
};
