/**
 * The single API surface. Split by feature to match the rest of `src/`, but
 * recomposed here: every caller imports `api` from `"../api"` and neither knows
 * nor cares which file an endpoint now lives in.
 */
import { agentsApi } from "./agents";
import { authApi } from "./auth";
import { copilotApi } from "./copilot";
import { foldersApi } from "./folders";
import { graphApi } from "./graph";
import { historyApi } from "./history";
import { lifecycleApi } from "./lifecycle";
import { nodesApi } from "./nodes";
import { notifyApi } from "./notify";
import { remoteApi } from "./remote";
import { runnersApi } from "./runners";
import { runsApi } from "./runs";
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
export type { Actor } from "./auth";
export {
  COPILOT_MODES,
  NdjsonParser,
  parseTurnEventLine,
  readTurnEvents,
  streamTurn,
} from "./copilot";
export type {
  CopilotClientTool,
  CopilotConversation,
  CopilotConversationDetail,
  CopilotDecision,
  CopilotEntry,
  CopilotEntryKind,
  CopilotMode,
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
export type { AuditEntry } from "./history";
export type { NotifySettings } from "./notify";
export type {
  DriveCredentialSource,
  DriveCredentialStatus,
  DriveFolder,
  RemoteBundle,
  RemoteConfig,
  RemoteKind,
  RemoteSyncState,
  RemoteSyncStatus,
} from "./remote";
export type {
  AgentAsk,
  AgentMessage,
  AgentRosterResponse,
  AgentSession,
} from "./agents";
export type {
  Runner,
  RunnerAgent,
  RunnerCreateResponse,
  RunnerInfo,
  RunnerListResponse,
  RunnerModel,
  RunnerRepo,
} from "./runners";
export {
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
} from "./runs";
export { connectWS, documentKey } from "./ws";
export type { DragGhost, LiveConnection, NodeLock, Peer, WSEvent } from "./ws";

export const api = {
  ...graphApi,
  ...nodesApi,
  ...foldersApi,
  ...lifecycleApi,
  ...workspaceApi,
  ...notifyApi,
  ...authApi,
  ...historyApi,
  ...remoteApi,
  ...copilotApi,
  ...agentsApi,
  ...runnersApi,
  ...runsApi,
};
