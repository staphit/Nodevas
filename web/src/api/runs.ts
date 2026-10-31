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

/** Whether a run waits, queued, for its agent's usage limit to reset. */
export function runWaitsForLimit(run: Pick<Run, "status" | "retryAt">): boolean {
  return run.status === "queued" && Boolean(run.retryAt);
}

/** Statuses the cancel route accepts. */
export function runCancellable(status: string): boolean {
  return status === "queued" || status === "offered" || status === "running";
}

/** Mirrors runnerwire.ModeHeadless ("") and ModeInteractive (W2.5). */
export type RunMode = "" | "interactive";
export const RUN_MODE_INTERACTIVE = "interactive";

/** Whether a run is interactive: its agent's own TUI in a PTY on the runner. */
export function runInteractive(run: { mode?: string }): boolean {
  return run.mode === RUN_MODE_INTERACTIVE;
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

/** One ask of a run as its timeline shows it (GET /api/runs/:id/asks):
 * what the run's agent waited on and how that ended. `preview` (a permission
 * prompt's input on one line, at most 120 characters) and `note` reach only
 * those who may answer the ask; everyone else sees the tool, or a question's
 * text, alone. Prompts allowed by an earlier "allow for this run" without
 * asking are not listed. */
export interface RunAsk {
  id: string;
  kind: "approval" | "question" | "choice" | "permission" | string;
  status: "pending" | "approved" | "denied" | "answered" | "expired" | string;
  /** approve, approve_run, deny or answer; absent while pending or expired. */
  decision?: string;
  /** An approval's, question's or choice's text on one line. */
  question?: string;
  tool?: string;
  preview?: string;
  note?: string;
  sessionName?: string;
  /** Unix ms, on the hub's clock. */
  createdAt: number;
  decidedAt?: number;
  /** Still waiting, and the viewer may answer it. */
  canAnswer: boolean;
}

export interface RunAsksResponse {
  asks: RunAsk[] | null;
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
  /** W5: "" (implement), "review" or "revise". */
  kind?: RunKind | string;
  /** W5: why a failed run failed, for the fallback chain. */
  failureKind?: FailureKind | string;
  /** W5: a review run's verdict. */
  verdict?: ReviewVerdict | string;
  /** W5: the run this one falls back from. */
  retryOf?: string;
  /** W5: the pipeline the run is a step of. */
  pipelineId?: string;
  /** W2.5: absent (headless) or "interactive". */
  mode?: RunMode | string;
  /** W7: a plan run that reconciles a goal's tasks with a revision of its
   * requirement (the voice copilot's revise_goal), not a first breakdown. */
  replan?: boolean;
  /** Handoff: the local branch the runner kept this run's partial work on
   * when it hit a usage limit, and its note for the next run. */
  handoffBranch?: string;
  handoffNote?: string;
  /** Handoff: the branch this run continues another run's work from. */
  continuesFrom?: string;
  /** While the run waits, queued, for its agent's usage limit to reset: when
   * it is offered again (RFC3339). Absent once released. */
  retryAt?: string;
  /** Agent roles: the role the run acted in (absent: legacy), its verify
   * result (red/green), a judge's checks, and a person's stage override. */
  role?: string;
  verify?: VerifyResult | null;
  checks?: CheckResult[] | null;
  override?: string;
  /** Agent analytics: the run's cost (the CLI's own figure, or estimated
   * from its tokens when costEstimated), and the node's type and acceptance
   * size when it was dispatched. Absent from an older hub. */
  costUsd?: number;
  costEstimated?: boolean;
  nodeType?: string;
  acceptanceCount?: number;
  createdBy: string;
  createdByName: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
}

/** Mirrors runnerwire.RoleSettings: one owner's concurrency and budget for a
 * role. Project "" applies in every project; 0 is no limit. */
export interface RoleSettings {
  role: string;
  project: string;
  maxConcurrent: number;
  dailyCostUsd: number;
  dailyTokens: number;
  dailyRuns: number;
}

export interface RoleSettingsResponse {
  roles: RoleSettings[] | null;
}

/** Mirrors runnerwire.VerifyResult: one run of the repo's test command. */
export interface VerifyResult {
  phase: "red" | "green" | "goal" | string;
  head?: string;
  exitCode: number;
  /** Relative to the phase: red passes when the tests fail. */
  passed: boolean;
  durationMs: number;
  output?: string;
  noCommand?: boolean;
}

/** Mirrors runnerwire.CheckResult: a judge's verdict on one criterion. */
export interface CheckResult {
  id: string;
  pass: boolean;
  note?: string;
}

/** Mirrors runnerwire.GuardReport: the executor diff check. */
export interface GuardReport {
  testFiles?: string[];
  touched?: string[];
  sensitive?: string[];
  approved?: boolean;
  approvedBy?: string;
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

/** Mirrors runnerwire.SessionChange (W6): what an agent the owner runs in
 * their own terminal (not through a runner) changed while it held a node. */
export interface SessionChange {
  id: string;
  project: string;
  nodeId: string;
  sessionId: string;
  /** The roster name, e.g. "visual-editor-claude". */
  session: string;
  /** The CLI, e.g. "claude". */
  agent: string;
  actorId: string;
  repo?: string;
  branch?: string;
  /** Files its edits touched, repo-relative when known. */
  files: string[] | null;
  /** The diff taken at Stop, when one was. */
  snapshot?: RunChanges | null;
  createdAt: string;
  updatedAt: string;
}

export interface SessionChangesResponse {
  changes: SessionChange[] | null;
}

/** runnerwire.MaxSessionFiles: the hub keeps at most this many paths. */
export const MAX_SESSION_FILES = 500;

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
  /** Tried in order when the run fails for a reason another model could fix
   * (W5). At most MAX_FALLBACKS. */
  fallbacks?: AgentModel[];
  /** Dispatches implement → review instead of one run (W5); agent and model
   * are then the hub's to ignore in favour of pipeline.implement. */
  pipeline?: RunPipeline;
  /** W2.5: omitted for headless. "interactive" needs the runner's
   * "interactive" capability; the hub answers 409 otherwise. */
  mode?: RunMode;
  /** Agent roles: the role the run acts in, and a person's reason for a
   * plain run past enabled stages (at most 200 characters). */
  role?: string;
  override?: string;
  /** The auto-routing pick this dispatch followed (RouteSuggestion.ruleId
   * "auto:<id>" without the prefix), so the hub links it to the run. */
  routePick?: string;
}

// ---- W5: routing, fallback and pipelines. Mirrors internal/runnerwire/routing.go.
// All of it is the owner's own configuration: there are no built-in routes.

/** One agent CLI and one of its models. */
export interface AgentModel {
  agent: string;
  model: string;
}

/** Route match kinds, tried in this order by the hub. */
export type RouteMatch = "tag" | "kind" | "project" | "default" | "role";
/** RouteSuggestion.match for an auto-routing pick; never a stored rule's. */
export const ROUTE_AUTO = "auto";
export const ROUTE_MATCHES: readonly RouteMatch[] = ["tag", "kind", "role", "project", "default"];

/** Mirrors runnerwire.RouteRule. Project "" applies in every project. */
export interface RouteRule {
  id: string;
  project: string;
  match: RouteMatch | string;
  /** The tag or kind matched; unused for project and default. */
  value?: string;
  /** "" means whichever runner the dispatcher picks. */
  runnerId?: string;
  target: AgentModel;
  fallbacks?: AgentModel[] | null;
  pipeline?: RunPipeline | null;
}

/** GET/PUT /api/runs/routing: the caller's rules, in order. */
export interface RoutingConfig {
  rules: RouteRule[] | null;
}

/** GET /api/runs/route?nodeId=: what the rules pick. ruleId "" = no match. */
export interface RouteSuggestion {
  ruleId: string;
  match: RouteMatch | string;
  /** Not in the wire contract; read when a hub sends it. */
  value?: string;
  runnerId?: string;
  target: AgentModel;
  fallbacks?: AgentModel[] | null;
  pipeline?: RunPipeline | null;
}

/** Mirrors runnerwire.Pipeline: implement → review, up to maxRounds. */
export interface RunPipeline {
  implement: AgentModel;
  review: AgentModel;
  maxRounds: number;
  /** W7: reviewers voting in parallel (review is the only one when empty),
   * how many approvals pass a round (0: all), and whether a person decides
   * after them. */
  reviewers?: AgentModel[] | null;
  quorum?: number;
  human?: boolean;
  /** Agent roles: who writes the failing tests of a staged pipeline. */
  tester?: AgentModel;
}

export type PipelineStatus =
  | "running"
  | "waiting_human"
  | "approved"
  | "changes_left"
  | "failed"
  | "cancelled";

/** One vote on a pipeline's PR in a round (W7). "abstain": the review run
 * failed or gave no verdict; "": still reviewing. A person's vote has agent
 * "human" and their name as the model. */
export interface PipelineVote {
  agent: string;
  model: string;
  verdict: "approve" | "changes" | "abstain" | "";
  runId?: string;
  round: number;
}

/** Mirrors runnerwire.PipelineState: GET /api/runs/pipelines/:id. */
export interface PipelineState {
  id: string;
  nodeId: string;
  pipeline: RunPipeline;
  round: number;
  status: PipelineStatus | string;
  runIds: string[] | null;
  createdAt: string;
  /** W7: the votes so far and a person's decision in the current round. */
  votes?: PipelineVote[] | null;
  humanVerdict?: string;
  /** Staged pipeline: the frozen stages, the phase, the Tester's files and
   * the executor diff guard. Absent on a legacy pipeline. */
  stages?: { preset: string; stages: Record<string, boolean>; judge?: string } | null;
  phase?: string;
  testFiles?: string[] | null;
  guard?: GuardReport | null;
}

/** Whether a pipeline will not change any more. */
export function pipelineEnded(status: string | undefined): boolean {
  return Boolean(status) && status !== "running" && status !== "waiting_human";
}

export type RunKind = "" | "review" | "revise" | "plan";
export type FailureKind =
  | "rate_limit"
  | "quota"
  | "model_unavailable"
  | "agent_unavailable"
  | "task";
export const FAILURE_KINDS: readonly FailureKind[] = [
  "rate_limit",
  "quota",
  "model_unavailable",
  "agent_unavailable",
  "task",
];
export type ReviewVerdict = "approve" | "changes";

export const MAX_ROUTE_RULES = 200;
export const MAX_FALLBACKS = 5;
export const MAX_PIPELINE_ROUNDS = 3;

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
  /** Every run of the project that has not ended (queued, offered,
   * running), uncapped, several on one node included. Same visibility as
   * the other lists: the caller's own runs and runs on their runners, or
   * everyone's for an administrator. */
  listActiveRuns: () => req<RunListResponse>("/api/runs?active=1"),
  getRun: (id: string) => req<Run>(runPath(id)),
  getRunEvents: (id: string, after = 0, limit = RUN_EVENTS_PAGE) =>
    req<RunEventsResponse>(`${runPath(id)}/events?after=${after}&limit=${limit}`),
  /** The run's asks for its timeline, oldest first (the newest 200). */
  getRunAsks: (id: string) => req<RunAsksResponse>(`${runPath(id)}/asks`),
  /** External sessions' changes on a node (W6), project-scoped like every
   * node route. */
  getSessionChanges: (nodeId: string) =>
    req<SessionChangesResponse>(`/api/nodes/${encodeURIComponent(nodeId)}/session-changes`),
  getRunChanges: (id: string) => req<RunChangesResponse>(`${runPath(id)}/changes`),
  cancelRun: (id: string) => req<Run>(`${runPath(id)}/cancel`, { method: "POST" }),
  /** Ends a run's wait for a usage limit to reset: it is offered as soon as
   * its runner has room (still within the budget). */
  retryRunNow: (id: string) => req<Run>(`${runPath(id)}/retry-now`, { method: "POST" }),
  /** Approves merging the run's PR at `headSha`; 202 with the run, its
   * merge "pending" until the runner answers. */
  mergeRun: (id: string, headSha: string) =>
    req<Run>(`${runPath(id)}/merge`, { method: "POST", body: JSON.stringify({ headSha }) }),
  getRunBudget: () => req<RunBudgetStatus>("/api/runs/budget"),
  putRunBudget: (budget: RunBudget) =>
    req<RunBudgetStatus>("/api/runs/budget", { method: "PUT", body: JSON.stringify(budget) }),
  getRouting: () => req<RoutingConfig>("/api/runs/routing"),
  putRouting: (rules: RouteRule[]) =>
    req<RoutingConfig>("/api/runs/routing", { method: "PUT", body: JSON.stringify({ rules }) }),
  /** Agent roles: the viewer's per-role concurrency and daily budget. */
  getRoleSettings: () => req<RoleSettingsResponse>("/api/runs/roles"),
  putRoleSettings: (roles: RoleSettings[]) =>
    req<RoleSettingsResponse>("/api/runs/roles", { method: "PUT", body: JSON.stringify({ roles }) }),
  /** What the routing rules pick for a node; ruleId "" when none matched. */
  getRoute: (nodeId: string) =>
    req<RouteSuggestion>(`/api/runs/route?nodeId=${encodeURIComponent(nodeId)}`),
  getPipeline: (id: string) =>
    req<PipelineState>(`/api/runs/pipelines/${encodeURIComponent(id)}`),
};
