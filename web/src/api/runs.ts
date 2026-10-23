import { req } from "./http";

// Mirrors internal/runnerwire/runs.go. A run is one agent CLI invocation on a
// runner, for one board node, in its own git worktree. The hub records and
// relays it; only the runner starts the agent, and only once someone at the
// runner accepted the offer.

export type RunStatus =
  | "queued"
  | "offered"
  | "declined"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled";

/** Statuses a run never leaves. Mirrors runnerwire.RunFinal. */
export function runFinal(status: string): boolean {
  return (
    status === "declined" ||
    status === "succeeded" ||
    status === "failed" ||
    status === "cancelled"
  );
}

/** Statuses the cancel route accepts. */
export function runCancellable(status: string): boolean {
  return status === "queued" || status === "offered" || status === "running";
}

export interface RunUsage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens?: number;
  cacheWriteTokens?: number;
  /** A reference figure only: runs use the owner's subscription login. */
  costUsd?: number;
}

export type RunEventType =
  | "text"
  | "thinking"
  | "tool_call"
  | "tool_result"
  | "usage"
  | "error"
  | "log";

export interface RunEvent {
  /** 1, 2, 3… per run, assigned by the runner. */
  seq: number;
  type: RunEventType | string;
  /** RFC3339 with milliseconds, on the runner's clock. */
  at: string;
  text?: string;
  tool?: string;
  toolId?: string;
  input?: string;
  isError?: boolean;
  usage?: RunUsage;
}

export interface Run {
  id: string;
  project: string;
  nodeId: string;
  runnerId: string;
  runnerName: string;
  agent: string;
  model: string;
  repoId: string;
  repoName: string;
  base: string;
  branch: string;
  /** The dispatcher's extra instructions, as typed. */
  prompt: string;
  status: RunStatus | string;
  error?: string;
  summary?: string;
  exitCode: number;
  usage?: RunUsage;
  /** The run's commit SHA; absent while running or when nothing changed. */
  commit?: string;
  filesChanged?: number;
  additions?: number;
  deletions?: number;
  prUrl?: string;
  prNumber?: number;
  prState?: PRState | string;
  /** Why no pull request could be opened; prUrl is then absent. */
  prError?: string;
  ciStatus?: CIStatus | string;
  /** The PR head the CI status belongs to; a merge approval names it. */
  prHeadSha?: string;
  /** The latest merge request for the run, if any (W4). */
  merge?: RunMergeRequest | null;
  createdBy: string;
  createdByName: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
}

/** Mirrors the runnerwire Merge* statuses. */
export type MergeStatus = "pending" | "merged" | "failed" | "expired";

/** Mirrors runnerwire.MergeRequest: one approval to merge a run's PR. The
 * runner merges it with its owner's gh login and its own merge method. */
export interface RunMergeRequest {
  id: string;
  runId: string;
  /** The PR head the approver saw; the runner refuses a PR that moved on. */
  headSha: string;
  status: MergeStatus | string;
  reason?: string;
  requestedBy: string;
  createdAt: string;
  finishedAt?: string;
}

/** Mirrors runnerwire.Budget: per owner, per UTC day; 0 means no limit. */
export interface RunBudget {
  dailyRuns: number;
  dailyCostUsd: number;
  /** Input + output tokens. */
  dailyTokens: number;
}

/** The limit that blocks a new run, or "" when none does. */
export type RunBudgetLimit = "" | "runs" | "cost" | "tokens";

/** Mirrors runnerwire.BudgetStatus: GET /api/runs/budget. */
export interface RunBudgetStatus {
  budget: RunBudget;
  used: { runs: number; costUsd: number; tokens: number };
  exceeded: RunBudgetLimit | string;
}

export type PRState = "open" | "merged" | "closed";
/** Rolled up over the PR's checks; "" while none reported. */
export type CIStatus = "" | "pending" | "success" | "failure";
export type FileChangeStatus = "added" | "modified" | "deleted" | "renamed";

/** Mirrors runnerwire.FileChange: one file in a run's commit. */
export interface FileChange {
  path: string;
  status: FileChangeStatus | string;
  /** Set for a rename. */
  oldPath?: string;
  additions: number;
  deletions: number;
  /** Binary files have no line counts. */
  binary?: boolean;
}

/** Mirrors runnerwire.RunChanges: `git diff <base>...<commit>`. */
export interface RunChanges {
  /** "" when the agent changed nothing. */
  commit: string;
  base: string;
  files: FileChange[] | null;
  additions: number;
  deletions: number;
  /** The unified diff, at most 1 MiB; absent when patchTruncated. */
  patch?: string;
  patchTruncated?: boolean;
}

export interface RunChangesResponse {
  /** null for a run that reported none (yet). */
  changes: RunChanges | null;
}

export interface CreateRunRequest {
  nodeId: string;
  runnerId: string;
  agent: string;
  model: string;
  repoId: string;
  /** Branch to start from; omitted means the repository's default. */
  base?: string;
  /** Extra instructions on top of the node's own content. */
  prompt?: string;
}

export interface RunListResponse {
  runs: Run[] | null;
}

export interface RunEventsResponse {
  events: RunEvent[] | null;
  /** True when the page was cut short; ask again after the last seq. */
  more: boolean;
}

/** Longest extra instructions the dispatch dialog accepts, in characters. */
export const MAX_RUN_PROMPT_CHARS = 8000;
/** The hub's own cap on CreateRunRequest.prompt, in UTF-8 bytes: 8000 CJK
 * characters are three times over it. */
export const MAX_RUN_PROMPT_BYTES = 8 << 10;

/** Events asked for per page: the hub's default (its maximum is 1000). */
export const RUN_EVENTS_PAGE = 500;

const runPath = (id: string) => `/api/runs/${encodeURIComponent(id)}`;

export const runsApi = {
  createRun: (body: CreateRunRequest) =>
    req<Run>("/api/runs", { method: "POST", body: JSON.stringify(body) }),
  listRuns: (nodeId: string) =>
    req<RunListResponse>(`/api/runs?nodeId=${encodeURIComponent(nodeId)}`),
  /** The project's most recent runs (the hub caps the list at 50). */
  listProjectRuns: () => req<RunListResponse>("/api/runs"),
  getRun: (id: string) => req<Run>(runPath(id)),
  getRunEvents: (id: string, after = 0, limit = RUN_EVENTS_PAGE) =>
    req<RunEventsResponse>(`${runPath(id)}/events?after=${after}&limit=${limit}`),
  getRunChanges: (id: string) => req<RunChangesResponse>(`${runPath(id)}/changes`),
  cancelRun: (id: string) => req<Run>(`${runPath(id)}/cancel`, { method: "POST" }),
  /** Approves merging the run's PR at `headSha`; 202 with the run, its
   * merge "pending" until the runner answers. */
  mergeRun: (id: string, headSha: string) =>
    req<Run>(`${runPath(id)}/merge`, { method: "POST", body: JSON.stringify({ headSha }) }),
  getRunBudget: () => req<RunBudgetStatus>("/api/runs/budget"),
  putRunBudget: (budget: RunBudget) =>
    req<RunBudgetStatus>("/api/runs/budget", { method: "PUT", body: JSON.stringify(budget) }),
};
