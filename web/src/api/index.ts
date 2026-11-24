/**
 * The single API surface. Split by feature to match the rest of `src/`, but
 * recomposed here: every caller imports `api` from `"../api"` and neither knows
 * nor cares which file an endpoint now lives in.
 */
import { accountsApi } from "./accounts";
import { agentsApi } from "./agents";
import { auditApi } from "./audit";
import { autopilotApi, planApi } from "./autopilot";
import { authApi } from "./auth";
import { contestsApi } from "./contests";
import { controlApi } from "./control";
import { copilotApi } from "./copilot";
import { foldersApi } from "./folders";
import { globalRunsApi } from "./globalRuns";
import { graphApi } from "./graph";
import { historyApi } from "./history";
import { lifecycleApi } from "./lifecycle";
import { membersApi } from "./members";
import { nodesApi } from "./nodes";
import { notifyApi } from "./notify";
import { remoteApi } from "./remote";
import { runnersApi } from "./runners";
import { runsApi } from "./runs";
import { stationApi } from "./station";
import { themesApi } from "./themes";
import { usageApi } from "./usage";
import { workspaceApi } from "./workspace";

export {
  AuthError,
  ConflictError,
  getProjectOverride,
  isNetworkFailure,
  NetworkError,
  onUnauthorized,
  RequestError,
  setProjectOverride,
  UnauthorizedError,
} from "./http";
export { ApiShapeError } from "./verify";
export { batchViaTaken } from "./graph";
export type {
  BatchRedoResult,
  BatchRefusal,
  BatchUndoResult,
  GraphOp,
  GraphOpsResponse,
  GraphResponse,
  SubgraphNodeSpec,
  SubgraphResult,
  SubgraphSpec,
} from "./graph";
export type {
  NodePageInfo,
  NodeTransferMode,
  NodeTransferRequest,
  NodeTransferResult,
  PageFormat,
} from "./nodes";
export { fileNameFromDisposition } from "./workspace";
export type {
  ExportedFile,
  ExportFormat,
  ExportRequest,
  ExportScope,
  ProjectImportMode,
} from "./workspace";
export type {
  Actor,
  LoginResult,
  StepUpMethod,
  StepUpStatus,
  TotpEnrollment,
  TotpState,
} from "./auth";
export {
  COPILOT_MODES,
  CopilotBusyError,
  NdjsonParser,
  parseTurnEventLine,
  readTurnEvents,
  streamTurn,
} from "./copilot";
export type {
  CopilotBrowserModel,
  CopilotClientTool,
  CopilotConversation,
  CopilotConversationDetail,
  CopilotDecision,
  CopilotDraft,
  CopilotDraftData,
  CopilotDraftExtras,
  CopilotDraftIssue,
  CopilotDraftNode,
  CopilotDraftOrigin,
  CopilotDraftState,
  CopilotDraftStep,
  CopilotIntent,
  CopilotEntry,
  CopilotEntryKind,
  CopilotMode,
  CopilotModelHost,
  CopilotModelRequest,
  CopilotSettings,
  CopilotSettingsInput,
  CopilotSource,
  CopilotStatus,
  CopilotToolKind,
  CopilotTurnContext,
  CopilotTurnEvent,
  CopilotTurnEventType,
  CopilotTurnRequest,
  CopilotView,
} from "./copilot";
export type {
  AuditEntry,
  AuditHealth,
  AuditHealthStatus,
  AuditLimiterStats,
  AuditQuery,
  AuditScope,
} from "./audit";
export type { MemberRole, ProjectAccess, ProjectMember, ProjectMembers } from "./members";
export type {
  InviteEmailStatus,
  InviteResult,
  ServerAccount,
  ServerAccounts,
  ServerRole,
  ServerSeats,
} from "./accounts";
export type { NotifySettings } from "./notify";
export type {
  RemoteBundle,
  RemoteConfig,
  RemoteGitProject,
  RemoteGitPush,
  RemoteKind,
  RemoteSyncState,
  RemoteSyncStatus,
} from "./remote";
export type {
  AgentAsk,
  AgentAskAnswerRequest,
  AgentAskDecision,
  AgentAskKind,
  AgentAskStatus,
  AgentFileUpload,
  AgentInbound,
  AgentMessage,
  AgentMessageFile,
  AgentMessageStatus,
  AgentRosterResponse,
  AgentSession,
} from "./agents";
export type {
  Runner,
  RunnerAgent,
  RunnerCreateResponse,
  RunnerLinkResponse,
  RunnerAccessTokenStatus,
  AutoAcceptPolicy,
  RunnerInfo,
  RunnerListResponse,
  RunnerModel,
  RunnerRepo,
  RunnerSettingsResponse,
  RunnerSettingsRequest,
  LocalSettings,
  PermissionLevel,
  InitPlan,
  RepoInspect,
  RepoInspectState,
  RepoRefusal,
  RepoAddResponse,
  RepoChangeResponse,
} from "./runners";
export {
  CAP_APP_ACCEPT,
  CAP_CONTINUE,
  CAP_COORD,
  CAP_GLOBAL_RUNS,
  CAP_GLOBAL_RUNS_ON,
  CAP_REMOTE_SETTINGS,
  CAP_REPO_ADMIN,
  CAP_TOUCHED,
  RunnerRequestError,
} from "./runners";
export {
  DEFAULT_GLOBAL_SLUG,
  globalTitle,
  GlobalRunError,
  MAX_FOLLOW_UP_BYTES,
  MAX_GLOBAL_TASK_BYTES,
  MAX_GLOBAL_TITLE_CHARS,
  MAX_GROUP_REPOS,
  MAX_SLUG_BYTES,
  slugFrom,
  validSlug,
} from "./globalRuns";
export type {
  CancelAllResponse,
  CreateGlobalGroupRequest,
  CreateGlobalRunRequest,
  FollowUpRequest,
  GlobalGroupResponse,
  GlobalRunsBlock,
  GlobalRunsResponse,
  GroupFollowUpRequest,
  GroupFollowUpResponse,
  GroupFollowUpSkipped,
  ResumeGlobalGroupResponse,
  RunGroup,
  RunnerLoad,
  RunnerToday,
  RunThreadResponse,
  SettableBlock,
} from "./globalRuns";
export {
  BusyError,
  MAX_HOLDER_IDS,
  MAX_SESSION_FILES,
  MAX_RUN_PROMPT_BYTES,
  MAX_RUN_PROMPT_CHARS,
  runCancellable,
  runWaitsForLimit,
  runFinal,
  forgetRunWorkspace,
  forgetGlobalRuns,
  isGlobalRun,
  rememberGlobalRun,
  rememberGlobalRuns,
  runPath,
  WorkspaceActionError,
  WORKSPACE_CODES,
  WORKSPACE_GATE_CODES,
  WORKSPACE_RUNNER_CODES,
} from "./runs";
export type {
  AskReason,
  BusyChoice,
  BusyContinue,
  BusyOption,
  BusyRepo,
  BusyResponse,
  GlobalBusyResponse,
  OfferAnswer,
  OfferAsk,
  OnBusy,
  RunEndReason,
  RunHolder,
  RunOrigin,
  RunOverlap,
  RunOverlapsResponse,
  CIStatus,
  CreateRunRequest,
  FileChange,
  FileChangeStatus,
  MergeStatus,
  PRState,
  Run,
  RunAsk,
  RunAsksResponse,
  RunBudget,
  RunBudgetLimit,
  RunBudgetStatus,
  RunChanges,
  RunChangesResponse,
  RunEvent,
  RunEventsResponse,
  RunEventType,
  RunListResponse,
  RunMergeRequest,
  RunStatus,
  RunUsage,
  SessionChange,
  SessionChangesResponse,
  AgentModel,
  FailureKind,
  PipelineState,
  PipelineVote,
  PipelineStatus,
  ReviewVerdict,
  RouteMatch,
  RouteRule,
  RouteSuggestion,
  RoutingConfig,
  RunKind,
  RunPipeline,
  CheckResult,
  GuardReport,
  RoleSettings,
  RoleSettingsResponse,
  VerifyResult,
  RunWorkspaceAction,
  RunWorkspaceInfo,
  RunWorkspaceState,
  RunWorkspaceView,
  WorkspaceActionKind,
  WorkspaceActionStatus,
  WorkspaceCode,
  WorkspaceFilesBlock,
  WorkspaceGateCode,
} from "./runs";
export {
  FAILURE_KINDS,
  MAX_FALLBACKS,
  MAX_PIPELINE_ROUNDS,
  MAX_ROUTE_RULES,
  pipelineEnded,
  ROUTE_AUTO,
  ROUTE_MATCHES,
} from "./runs";
export { MAX_USAGE_DAYS, USAGE_RANGES } from "./usage";
export type {
  ModelPrice,
  PriceSource,
  UsageBucket,
  UsageGroup,
  UsageModelGroup,
  UsagePrices,
  UsageReport,
  UsageSession,
  UsageSource,
} from "./usage";
export {
  autopilotEnded,
  MAX_AUTOPILOT_PARALLEL,
  MAX_REVIEWERS,
  REVIEW_MODES,
  reviewHasAgents,
  reviewHasHuman,
  STAGE_STATUSES,
  TASK_STAGES,
  TASK_STATES,
} from "./autopilot";
export type {
  AutopilotAction,
  AutopilotAdvisor,
  AutopilotListResponse,
  AutopilotPolicy,
  AutopilotRun,
  AutopilotStatus,
  AutopilotTaskAction,
  AutopilotView,
  CreateAutopilotRequest,
  HumanReviewRequest,
  LiveAutopilotStatus,
  PlanRunRequest,
  ReviewMode,
  ReviewPolicy,
  ReviewVote,
  StageRecord,
  StageStatus,
  TaskCounts,
  TaskInstance,
  TaskStage,
  TaskState,
} from "./autopilot";
export type {
  ControlSkipReason,
  ControlTarget,
  ControlTargetKind,
  PauseAllRequest,
  PauseAllResponse,
} from "./control";
export type {
  BuildingAsk,
  BuildingProject,
  BuildingResponse,
  BuildingReview,
  BuildingRun,
  BuildingSession,
  BuildingWaiting,
  WaitingRunLike,
} from "./station";
export type { ThemePack, ThemePackList } from "./themes";
export { connectWS, documentKey } from "./ws";
export type { DragGhost, LiveConnection, NodeLock, Peer, WSEvent } from "./ws";
// W2.5: interactive runs and their terminal socket.
export { RUN_MODE_INTERACTIVE, runInteractive } from "./runs";
export type { RunMode } from "./runs";
export { MAX_CONTEST_ENTRIES } from "./contests";
export type {
  Contest,
  ContestApply,
  ContestCompare,
  ContestCompareEntry,
  ContestDetailResponse,
  ContestEntrySpec,
  ContestLabel,
  ContestResponse,
  ContestReviewer,
  ContestScore,
  ContestScores,
  ContestState,
  CreateContestRequest,
  CreateGlobalContestRequest,
  GlobalContestEntrySpec,
  JudgeContestRequest,
  JudgeContestResponse,
  PickActionResult,
  PickContestRequest,
  PickContestResponse,
} from "./contests";
export {
  connectTerminal,
  MAX_TERM_CHUNK,
  MAX_TERM_COLS,
  MAX_TERM_ROWS,
  terminalSocketURL,
} from "./terminal";
export type { TerminalConnection, TerminalHandlers, TerminalOptions, TerminalState } from "./terminal";

export const api = {
  ...graphApi,
  ...nodesApi,
  ...foldersApi,
  ...lifecycleApi,
  ...workspaceApi,
  ...notifyApi,
  ...authApi,
  ...historyApi,
  ...auditApi,
  ...remoteApi,
  ...copilotApi,
  ...agentsApi,
  ...runnersApi,
  ...runsApi,
  ...globalRunsApi,
  ...contestsApi,
  ...usageApi,
  ...membersApi,
  ...accountsApi,
  ...autopilotApi,
  ...planApi,
  ...controlApi,
  ...stationApi,
  ...themesApi,
};
