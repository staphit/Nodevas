import { explorerGet, forgetReads, remembered, type RepoFile, type RepoGitStatus, type RepoListing } from "./explorer";
import { req, RequestError, send } from "./http";

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

/** Mirrors runnerwire.AskReason: one reason a runner asks before it starts
 * a run (the AskReason codes: shell_agent, ask_every_run, not_on_list,
 * needs_step_up, verify_tests). */
export interface AskReason {
  code: string;
  /** shell_agent: the agent kind. */
  agent?: string;
  /** shell_agent: the exact setting ("codexSandbox=danger-full-access"). */
  setting?: string;
  /** not_on_list: "repo" | "agent" | "mode". */
  missing?: string;
}

/** Mirrors runnerwire.OfferAsk: the runner waits for an answer before it
 * starts the run. `where` "app": this computer's built-in runner, answered
 * in the app by its owner; "runner": on that PC (tray or terminal). */
export interface OfferAsk {
  runId: string;
  where: "app" | "runner" | string;
  reasons: AskReason[] | null;
  /** RFC3339: no answer by then and it does not start. */
  expiresAt: string;
  /** The PC it waits on, as the runner names it. */
  machine?: string;
}

/** Mirrors runnerwire.OfferAnswer's web half: POST /api/runs/:id/offer. */
export interface OfferAnswer {
  start: boolean;
  /** Ticked "don't ask again for this agent's automatic runs". */
  alwaysAllowShell?: boolean;
}

/** Mirrors the runnerwire Origin* values: who or what started a run. ""
 * (unknown) counts as automatic. */
export type RunOrigin = "person" | "autopilot" | "pipeline" | "handoff" | "";

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
  /** More about failureKind: "signed_out" for an agent_unavailable run whose
   * agent is on its machine but not signed in (runs/signedOut.ts). */
  failureCode?: string;
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
  /** Agent workspace: "" while the run's worktree and branch are as the run
   * left them, "merged_local" once its runner merged the branch into its
   * base, "discarded" once its owner threw the work away. Not a status: the
   * run's outcome stays what it was. */
  workspace?: RunWorkspaceState | string;
  /** The base's head after a local merge. */
  workspaceCommit?: string;
  /** The latest workspace action on the run, like `merge`. */
  workspaceAction?: RunWorkspaceAction | null;
  /** Global agents (phase 2): a run in one of its runner's repositories for
   * no project and no node (project and nodeId are ""), started by the
   * runner's owner. Its routes live under /api/global-runs. */
  global?: boolean;
  /** A global run's title: its task's first line (at most 120 characters). */
  title?: string;
  /** A global run asked for no push and no pull request: its branch stays
   * on the runner for a local merge or a discard. */
  noPR?: boolean;
  /** Single-user edition: the runner waits for an answer before it starts
   * the run (an offered run only); absent while nothing is asked. */
  ask?: OfferAsk | null;
  /** Who or what started the run (RunOrigin). */
  origin?: RunOrigin | string;
  /** A declined run: why, as a runnerwire decline code (declined_in_app,
   * timed_out, repo_not_added…). */
  declineCode?: string;
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

// ---- Agent workspace (phase 1). Mirrors internal/runnerwire/workspace.go.

export type RunWorkspaceState = "" | "merged_local" | "discarded";
export type WorkspaceActionKind = "merge_local" | "discard";
export type WorkspaceActionStatus = "pending" | "done" | "failed" | "expired";

/** Why the hub will not offer an action now (RunWorkspaceView gates). */
export const WORKSPACE_GATE_CODES = [
  "not_owner",
  "running",
  "discarded",
  "merged_local",
  "pr_merged",
  "pending",
  "kind",
  "no_commit",
  "outdated",
  "actions_off",
  "offline",
  "in_use",
] as const;
export type WorkspaceGateCode = (typeof WORKSPACE_GATE_CODES)[number];

/** Why the runner refused or failed an action it was asked for. */
export const WORKSPACE_RUNNER_CODES = [
  "refused",
  "actions_off",
  "running",
  "not_own_branch",
  "has_pr",
  "no_branch",
  "no_base",
  "head_moved",
  "unsaved_commits",
  "dirty_worktree",
  "dirty_base",
  "base_elsewhere",
  "merge_driver",
  "signing_program",
  "conflict",
  "foreign_worktree",
  "pr_close_failed",
  "files_busy",
  "git",
] as const;
export type WorkspaceRunnerCode = (typeof WORKSPACE_RUNNER_CODES)[number];

/** Every code a workspace action can come back with, plus "expired" for one
 * the runner never answered. */
export type WorkspaceCode = WorkspaceGateCode | "head_moved" | WorkspaceRunnerCode | "expired";
export const WORKSPACE_CODES: readonly WorkspaceCode[] = [
  ...new Set<WorkspaceCode>([...WORKSPACE_GATE_CODES, "head_moved", ...WORKSPACE_RUNNER_CODES, "expired"]),
];

/** Mirrors runnerwire.RunWorkspaceAction: one merge or discard request. */
export interface RunWorkspaceAction {
  id: string;
  runId: string;
  action: WorkspaceActionKind | string;
  status: WorkspaceActionStatus | string;
  code?: WorkspaceCode | string;
  /** The runner's English detail, shown under the translated code. */
  reason?: string;
  /** Done, with a caveat ("worktree kept: ..."). */
  note?: string;
  headSha?: string;
  base?: string;
  mergeCommit?: string;
  requestedBy: string;
  createdAt: string;
  finishedAt?: string;
}

/** Mirrors runnerwire.RunWorkspaceInfo: the run's worktree and branch as its
 * runner sees them now. */
export interface RunWorkspaceInfo {
  exists: boolean;
  path?: string;
  branch: string;
  head?: string;
  ownBranch: boolean;
  branchExists: boolean;
  branchHead?: string;
  /** -1 when the runner could not read the worktree's status. */
  uncommitted: number;
  running: boolean;
  kept: boolean;
  baseCheckout: "repo" | "elsewhere" | "none" | "missing" | string;
  baseClean: boolean;
  watchedPr?: string;
}

export type WorkspaceFilesBlock = "" | "not_owner" | "offline" | "browse_off" | "outdated" | "no_worktree";

/** GET /api/runs/:id/workspace: the panel's header and what it may offer. */
export interface RunWorkspaceView {
  runId: string;
  /** The viewer owns the run's runner: actions and Files are theirs. */
  owner: boolean;
  runnerOnline: boolean;
  files: { allowed: boolean; blocked: WorkspaceFilesBlock | string };
  merge: { mode: "pr" | "local" | ""; blocked: WorkspaceGateCode | "" | string };
  discard: { allowed: boolean; blocked: WorkspaceGateCode | "" | string };
  info: RunWorkspaceInfo | null;
  infoError: "" | "offline" | "outdated" | "timeout" | "not_owner" | string;
}

/** A refused workspace action: the hub's code, and for a 409 the gate that
 * blocked it (`blocked`). */
export class WorkspaceActionError extends Error {
  status: number;
  code: string;
  blocked: string;
  constructor(status: number, code: string, blocked: string, message: string) {
    super(message);
    this.name = "WorkspaceActionError";
    this.status = status;
    this.code = code;
    this.blocked = blocked;
  }
}

/** How long a workspace POST may take: the hub waits up to 30 s for the
 * runner before it answers 202, so the usual 30 s limit would cut it off
 * just before that answer. */
const WORKSPACE_POST_TIMEOUT_MS = 45_000;

/** POSTs a workspace action. 200 (answered) and 202 (still pending) both
 * carry the run (a preview, its own answer); a refusal keeps its code and
 * gate, which req() drops. */
async function workspacePost<T = Run>(url: string, body: unknown): Promise<T> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), WORKSPACE_POST_TIMEOUT_MS);
  let res: Response;
  try {
    res = await send(url, { method: "POST", body: JSON.stringify(body), signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
  if (!res.ok) {
    const parsed = (await res.json().catch(() => ({}))) as { error?: unknown; code?: unknown; blocked?: unknown };
    throw new WorkspaceActionError(
      res.status,
      typeof parsed.code === "string" ? parsed.code : "",
      typeof parsed.blocked === "string" ? parsed.blocked : "",
      typeof parsed.error === "string" && parsed.error ? parsed.error : `${res.status}`,
    );
  }
  return (await res.json()) as T;
}

/** POST …/workspace/preview: a look at a finished run's files. A web page
 * opens at `url`, on this computer's own preview listener under a
 * short-lived token; anything else is `web: false`, and `folder` is the run
 * folder to open instead. A refusal is a WorkspaceActionError whose
 * `blocked` is running, merged_local, discarded, no_worktree, offline,
 * outdated or preview_off. */
export interface RunPreview {
  web: boolean;
  url?: string;
  folder?: string;
  expiresAt?: string;
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
  /** The unified diff, at most 1 MiB; absent when patchTruncated or patchPruned. */
  patch?: string;
  /** The diff was too big to keep. */
  patchTruncated?: boolean;
  /** History retention cleared the diff; the files and counts are kept. */
  patchPruned?: boolean;
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
  /** Simple mode: the hub adds its fixed plain-words instruction for someone
   * who does not write code (implement runs only). Lifts no gate: a run a
   * gate blocks still needs `override`. */
  simple?: boolean;
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

// Global agents: their per-run routes mirror /api/runs/:id under
// /api/global-runs/:id, and each prefix answers 404 for the other kind. A
// run id is known to be global once a list, a fetch or a realtime event said
// so; every per-run call below follows it from then on.
const globalRunIds = new Set<string>();

/** Remembers that `id` is a global run: its routes are /api/global-runs/:id. */
export function rememberGlobalRun(id: string | undefined): void {
  if (id) globalRunIds.add(id);
}

/** Remembers every global run among `runs`. */
export function rememberGlobalRuns(runs: ReadonlyArray<Pick<Run, "id" | "global"> | null | undefined>): void {
  for (const run of runs) if (run?.global) globalRunIds.add(run.id);
}

/** Whether `id` is a run known to be global. */
export function isGlobalRun(id: string): boolean {
  return globalRunIds.has(id);
}

/** Tests only: forget the global run ids. */
export function forgetGlobalRuns(): void {
  globalRunIds.clear();
}

/** A run's own route: /api/global-runs/:id for a global run, else /api/runs/:id. */
export function runPath(id: string): string {
  const prefix = globalRunIds.has(id) ? "/api/global-runs" : "/api/runs";
  return `${prefix}/${encodeURIComponent(id)}`;
}

const workspacePath = (id: string) => `${runPath(id)}/workspace`;

/** A run fetched by id, remembered as global when it is one. */
async function fetchRun(id: string): Promise<Run> {
  const run = await req<Run>(runPath(id));
  rememberGlobalRuns([run]);
  return run;
}

/** Drops a run's remembered workspace reads: after a merge or discard, or a
 * Refresh, the worktree is not what was read. */
export function forgetRunWorkspace(runId: string): void {
  forgetReads(`${workspacePath(runId)}/`);
}

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
  getRun: (id: string) => fetchRun(id),
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
  /** Answers the built-in runner's ask (its owner, in the app): start the
   * run or not. A refusal is a RequestError whose code is the hub's
   * (not_waiting, approve_on_runner…). The run as the hub has it after the
   * answer, or null when the hub sent none (the realtime push follows). */
  answerOffer: async (id: string, answer: OfferAnswer): Promise<Run | null> => {
    const res = await send(`${runPath(id)}/offer`, { method: "POST", body: JSON.stringify(answer) });
    const body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) {
      const text = (key: string) => (typeof body[key] === "string" ? (body[key] as string) : "");
      throw new RequestError(res.status, text("error") || `${res.status}`, text("code") || text("blocked"));
    }
    const run = (typeof body.id === "string" ? body : body.run) as Run | undefined;
    if (!run || typeof run.id !== "string") return null;
    rememberGlobalRuns([run]);
    return run;
  },
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
  /** The workspace panel's header and gates; the live worktree info only
   * for the runner's owner. */
  getRunWorkspace: (id: string) =>
    req<RunWorkspaceView>(workspacePath(id)),
  /** One folder of the run's live worktree, read through its runner (owner
   * only). Refusals are ExplorerErrors, as in the Explorer. */
  workspaceTree: (id: string, path: string): Promise<RepoListing> => {
    const url = `${workspacePath(id)}/tree?path=${encodeURIComponent(path)}`;
    return remembered(url, () =>
      explorerGet<RepoListing>(url).then((r) => ({ ...r, entries: r.entries ?? [] })),
    );
  },
  workspaceFile: (id: string, path: string): Promise<RepoFile> => {
    const url = `${workspacePath(id)}/file?path=${encodeURIComponent(path)}`;
    return remembered(url, () => explorerGet<RepoFile>(url));
  },
  /** Uncommitted changes in the run's worktree, against its HEAD. */
  workspaceStatus: (id: string): Promise<RepoGitStatus> =>
    explorerGet<RepoGitStatus>(`${workspacePath(id)}/git/status`).then((r) => ({
      ...r,
      entries: r.entries ?? [],
    })),
  /** Asks the runner to merge the run's branch into its base locally, at
   * the run commit the owner saw. 200 answered, 202 still pending. */
  mergeRunLocal: (id: string, headSha: string) =>
    workspacePost(`${workspacePath(id)}/merge`, { headSha }),
  /** Asks the runner to remove the run's worktree and branch (and close its
   * PR). Idempotent. */
  discardRun: (id: string) => workspacePost(`${workspacePath(id)}/discard`, {}),
  /** Asks this computer's built-in runner for a look at the run's files
   * (owner only). */
  previewRun: (id: string) => workspacePost<RunPreview>(`${workspacePath(id)}/preview`, {}),
};
