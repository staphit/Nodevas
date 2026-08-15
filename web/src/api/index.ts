/**
 * The single API surface. Split by feature to match the rest of `src/`, but
 * recomposed here: every caller imports `api` from `"../api"` and neither knows
 * nor cares which file an endpoint now lives in.
 */
import { authApi } from "./auth";
import { foldersApi } from "./folders";
import { graphApi } from "./graph";
import { historyApi } from "./history";
import { lifecycleApi } from "./lifecycle";
import { nodesApi } from "./nodes";
import { notifyApi } from "./notify";
import { remoteApi } from "./remote";
import { workspaceApi } from "./workspace";

export {
  AuthError,
  ConflictError,
  onUnauthorized,
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
};
