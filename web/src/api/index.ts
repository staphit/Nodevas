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
import { copilotApi } from "./copilot";
import { foldersApi } from "./folders";
import { graphApi } from "./graph";
import { historyApi } from "./history";
import { lifecycleApi } from "./lifecycle";
import { membersApi } from "./members";
import { nodesApi } from "./nodes";
import { notifyApi } from "./notify";
import { packsApi } from "./packs";
import { remoteApi } from "./remote";
import { runnersApi } from "./runners";
import { runsApi } from "./runs";
import { usageApi } from "./usage";
import { workspaceApi } from "./workspace";

export {
  AuthError,
  ConflictError,
  getProjectOverride,
  onUnauthorized,
  RequestError,
  setProjectOverride,
  UnauthorizedError,
} from "./http";
export { ApiShapeError } from "./verify";
export type { ViewPack, ViewPackLayout } from "./packs";
export type { GraphOp, GraphOpsResponse, GraphResponse } from "./graph";
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
} from "./runners";
export {
  MAX_SESSION_FILES,
  MAX_RUN_PROMPT_BYTES,
  MAX_RUN_PROMPT_CHARS,
  runCancellable,
  runFinal,
} from "./runs";
export type {
  CIStatus,
  CreateRunRequest,
  FileChange,
  FileChangeStatus,
  MergeStatus,
  PRState,
  Run,
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
} from "./runs";
export {
  FAILURE_KINDS,
  MAX_FALLBACKS,
  MAX_PIPELINE_ROUNDS,
  MAX_ROUTE_RULES,
  pipelineEnded,
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
export { connectWS, documentKey } from "./ws";
export type { DragGhost, LiveConnection, NodeLock, Peer, WSEvent } from "./ws";
// W2.5: interactive runs and their terminal socket.
export { RUN_MODE_INTERACTIVE, runInteractive } from "./runs";
export type { RunMode } from "./runs";
export {
  connectTerminal,
  MAX_TERM_CHUNK,
  MAX_TERM_COLS,
  MAX_TERM_ROWS,
  terminalSocketURL,
} from "./terminal";
export type { TerminalConnection, TerminalHandlers, TerminalState } from "./terminal";

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
  ...usageApi,
  ...membersApi,
  ...accountsApi,
  ...packsApi,
  ...autopilotApi,
  ...planApi,
};
