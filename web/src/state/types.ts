/**
 * Store shape [A-05].
 *
 * `AppState` is the intersection of the slices. It stays one flat object on
 * purpose: `useApp` is the compatibility facade every existing component reads
 * from, so splitting the *implementation* must not split the *surface*.
 */

import type { StateCreator } from "zustand";
import type {
  DriveCredentialStatus,
  DragGhost,
  GraphOp,
  LiveConnection,
  NodeLock,
  NodeTransferMode,
  NodeTransferRequest,
  NodeTransferResult,
  NotifySettings,
  PageFormat,
  Peer,
  RemoteBundle,
  RemoteConfig,
  RemoteKind,
  RemoteSyncStatus,
} from "../api";
import type {
  CanvasCommand,
  GraphCommand,
  MilestoneType,
  NodeCommand,
  PlanCommand,
  WorkflowCommand,
} from "../domain";
import type { PresenceState } from "../collab/presence";
import type { NodeMetadataPatch } from "../domain/graph/node";
import type { PlanMilestoneInput } from "../domain/graph/plan";
import type {
  PreferenceKey,
  PreferenceScope,
  PreferenceValue,
  PreferenceValues,
} from "../preferences";
import type {
  Graph,
  Issue,
  NodeStyle,
  PlanMilestone,
  PlanStatusDefinition,
  StatusDefinition,
  RunState,
  Status,
} from "../types";
import type {
  CommandResult,
  OperationMap,
  OperationScope,
  OperationSnapshot,
  OperationState,
} from "./operations";

export type Theme = "light" | "dark";

export interface Tab {
  id: string; // node id
  /** Pinned tabs stay at the leading edge and survive bulk close actions. */
  pinned: boolean;
  content: string;
  rev: string; // rev of the version this tab loaded/saved
  dirty: boolean;
  loaded: boolean;
  /** disk changed underneath a dirty tab (ws event) */
  diskChanged: boolean;
  /** unsaved draft found on open, awaiting user decision */
  draftPending: string | null;
  /** save hit a 409: holds the disk version for the user to resolve */
  conflict: { diskRev: string; diskContent: string } | null;
}

export interface TrashItem {
  file: string;
  kind: "node" | "page";
  nodeId: string;
  parentId?: string;
  title?: string;
  at: string;
}

export interface WorkspaceEntry {
  path: string;
  label: string;
  active: boolean;
  projects: number;
}

export interface ProjectEntry {
  name: string;
  label: string;
  parent?: string;
  depth: number;
  path: string;
  nodes: number;
  isFolder?: boolean;
}

export interface OperationsSlice {
  /** Per-scope mutation state [A-02]. Read it with `useOperation`. */
  operations: OperationMap;
  /** Newest transition of any scope, for a single global status line. */
  lastOperation: OperationSnapshot | null;
  beginOperation: (scope: OperationScope, operation: string) => void;
  settleOperation: (scope: OperationScope, next: OperationState) => void;
  clearOperation: (scope: OperationScope) => void;
}

export interface PreferencesSlice {
  theme: Theme;
  toggleTheme: () => void;
  paneOpen: { graph: boolean; timeline: boolean };
  togglePane: (p: "graph" | "timeline") => void;
  /** Local, per-machine UI preferences [A-03]. Never written to the project. */
  preferences: PreferenceValues;
  updateUIPreference: <K extends PreferenceKey>(
    key: K,
    value: PreferenceValue<K>,
  ) => void;
  resetUIPreferences: (scope?: PreferenceScope) => void;
}

export interface WorkspaceSlice {
  workspace: string;
  /**
   * Lifecycle states shared by every project in the workspace. The server
   * merges them into each graph, so this list is what the settings screen
   * edits, not what the board reads.
   */
  workspaceStatuses: StatusDefinition[];
  refreshWorkspaceStatuses: () => Promise<void>;
  saveWorkspaceStatuses: (statuses: StatusDefinition[]) => Promise<void>;
  workspaces: WorkspaceEntry[];
  activeProject: string;
  projects: ProjectEntry[];
  /**
   * The explorer's manual project order for this workspace, as a flattened
   * depth-first list of project names. Advisory: it may name projects that no
   * longer exist and omit ones that do, so only `sortProjectTree`'s `manual`
   * mode interprets it.
   */
  projectOrder: string[];
  saveProjectOrder: (order: string[]) => Promise<void>;
  trash: TrashItem[];
  refreshTrash: () => Promise<void>;
  refreshProjects: () => Promise<void>;
  addWorkspace: (path: string) => Promise<{ path: string; label: string }>;
  switchWorkspace: (path: string) => Promise<void>;
  removeWorkspace: (path: string) => Promise<{ workspace: string }>;
  moveProjectToWorkspace: (
    name: string,
    path: string,
  ) => Promise<{ name: string; workspace: string }>;
  switchProject: (name: string, create?: boolean, template?: string) => Promise<void>;
}

/**
 * A subpage open in an editor. Subpages live in the store rather than in the
 * editor component so a project-wide save can reach them: an editor registers
 * its open page under its own key and drops it on unmount.
 */
export interface PageDoc {
  /** The node the page belongs to — the save endpoint needs both ids. */
  nodeId: string;
  id: string;
  content: string;
  rev: string;
  format: PageFormat;
  dirty: boolean;
  loading: boolean;
  conflict: { diskRev: string; diskContent: string } | null;
}

export interface PageSaveResult {
  ok: boolean;
  /** Set when the save failed for a reason other than a disk conflict. */
  error?: string;
}

export interface SaveTabOptions {
  /**
   * True when nobody asked for this save — the idle timer fired, the field lost
   * focus, the tab is being left. It changes three things: a staged lifecycle
   * status is left staged, a conflict parks the text as a draft instead of
   * competing for attention, and a document already sitting on an unresolved
   * conflict is not written at all.
   */
  auto?: boolean;
  /**
   * True when the page itself is going away. The request has to outlive the
   * document that made it, which browsers only allow for a small body, so this
   * is a hint and not a promise — see `saveTab`.
   */
  unload?: boolean;
  /**
   * Whether an automatic save should park its snapshot as a draft when the
   * file write fails. Defaults to true. Batch close disables this fallback so
   * it can verify the draft write itself before removing the tab.
   */
  fallbackToDraft?: boolean;
}

export interface DocumentSlice {
  tabs: Tab[];
  activeTab: string | null; // null = drawer closed
  /** Open subpages, keyed by editor instance. */
  pageDocs: Record<string, PageDoc>;
  setPageDoc: (
    key: string,
    next: PageDoc | null | ((current: PageDoc | null) => PageDoc | null),
  ) => void;
  /** Writes one open subpage; a clean or loading page counts as saved. */
  savePageDoc: (key: string) => Promise<PageSaveResult>;
  openTab: (id: string) => Promise<void>;
  /**
   * Follows a `[[…]]` link: switches project first when the target lives in
   * another one, then opens the node. Rejects with a readable message when the
   * target is gone, so a stale link reports itself instead of doing nothing.
   */
  openNodeLink: (target: { project: string; nodeId: string }) => Promise<void>;
  /**
   * A node the board should select and scroll to. Set by anything that jumps
   * somewhere — following a link, opening a search hit — and cleared by the
   * board once it has moved. `at` makes two jumps to the same node distinct.
   */
  revealRequest: { nodeId: string; at: number } | null;
  revealNode: (nodeId: string) => void;
  clearRevealRequest: () => void;
  closeTab: (id: string) => void;
  /**
   * Saves dirty documents, parks failed writes as drafts, and closes only the
   * tabs whose latest content is recoverable. Rejects when any tab is retained.
   */
  closeTabs: (ids: string[]) => Promise<void>;
  setTabPinned: (id: string, pinned: boolean) => void;
  closeDrawer: () => void;
  setActiveTab: (id: string) => void;
  setTabContent: (id: string, content: string) => void;
  /**
   * Adopts a revision this window did not write itself: a live session's
   * leader wrote it, or the session merged the file at it. `settled` false
   * means the document has moved on since. See the slice.
   */
  adoptDocumentRev: (id: string, rev: string, settled?: boolean) => void;
  notifyDiskChange: (id: string) => Promise<void>;
  saveTab: (id: string, options?: SaveTabOptions) => Promise<void>;
  /**
   * Saves the document being navigated away from. `next` is where the user is
   * going, so switching to the document already open is not a save.
   */
  flushActiveDocument: (next: string | null) => void;
  /** Saves every dirty document; resolves with how many were written. */
  saveAllTabs: () => Promise<number>;
  saveDraft: (id: string) => Promise<void>;
  applyDraft: (id: string, use: boolean) => Promise<void>;
  resolveConflict: (id: string, resolution: "disk" | "mine") => Promise<void>;
}

export interface RunSlice {
  runState: RunState | null;
  statuses: Record<string, Status>;
  /**
   * Status changes chosen but not yet written, keyed by node [B-04]. They are
   * committed by whichever save the user reaches for, so a status change never
   * needs a save of its own.
   */
  stagedLifecycle: Record<string, { status: Status; note: string }>;
  /** Stages a change; an empty status clears the node's staged change. */
  stageLifecycleStatus: (nodeId: string, status: Status | "", note: string) => void;
  /** Writes the staged change, if any. Returns null when nothing was staged. */
  commitStagedLifecycle: (nodeId: string) => Promise<CommandResult | null>;
  refreshState: () => Promise<void>;
  /** Appends an actual lifecycle event to the journal. */
  setLifecycleStatus: (
    nodeId: string,
    status: Status,
    note?: string,
    options?: { recordUndo?: boolean },
  ) => Promise<CommandResult>;
  /** Appends a correction event; the original entry stays in the journal. */
  moveActualEvent: (
    eventId: string,
    timestamp: string,
    note?: string,
    options?: { recordUndo?: boolean },
  ) => Promise<CommandResult>;
  /** Compatibility wrapper: throws instead of returning a result. */
  setStatus: (id: string, status: Status, note?: string) => Promise<void>;
  /** Compatibility wrapper: throws instead of returning a result. */
  moveStamp: (eventId: string, t: string) => Promise<void>;
}

export interface GraphSlice {
  error: string | null;
  clearError: () => void;
  graph: Graph | null;
  graphRev: string;
  issues: Issue[];
  loadAll: () => Promise<void>;

  // --- typed domain commands [A-04] ---------------------------------------
  // These never throw: failures come back as `CommandResult` so a panel can
  // render the message where the edit happened.

  /**
   * Replays the ops a peer just applied. False means this client could not —
   * an op it does not know, or one about a node it has not seen — and the
   * caller should reload instead.
   */
  applyPeerGraphOps: (ops: GraphOp[]) => boolean;

  /** Applies one command: clone → validate → PUT → rollback on failure. */
  runGraphCommand: (
    command: GraphCommand,
    options?: { recordUndo?: boolean },
  ) => Promise<CommandResult>;
  updateNodeMetadata: (
    nodeId: string,
    patch: NodeMetadataPatch,
  ) => Promise<CommandResult>;
  updateNode: (command: NodeCommand) => Promise<CommandResult>;
  upsertPlanMilestone: (
    nodeId: string,
    milestone: PlanMilestoneInput,
  ) => Promise<CommandResult>;
  removePlanMilestone: (
    nodeId: string,
    milestoneType: MilestoneType,
  ) => Promise<CommandResult>;
  updatePlan: (command: PlanCommand) => Promise<CommandResult>;
  updateWorkflowDefinition: (command: WorkflowCommand) => Promise<CommandResult>;
  updateCanvasLayout: (command: CanvasCommand) => Promise<CommandResult>;

  commitRequires: (id: string, requires: string) => Promise<void>;
  createNode: (
    node: { id?: string; title?: string; kind?: string },
    options?: {
      openDocument?: boolean;
      position?: { x: number; y: number };
      plans?: PlanMilestone[];
      planStatuses?: PlanStatusDefinition[];
      /** Card appearance, written into `ui.nodeStyles` by the same graph save. */
      style?: NodeStyle;
    },
  ) => Promise<string>;
  duplicateNode: (id: string) => Promise<string>;
  deleteNode: (id: string) => Promise<void>;
  /** Deletes a whole selection in one graph write. */
  deleteNodes: (ids: string[]) => Promise<void>;
  /**
   * What a copy or cut is holding, until it is pasted or replaced. Node ids
   * only mean something inside their own project, so the project travels with
   * them.
   */
  nodeClipboard: NodeClipboard | null;
  setNodeClipboard: (clipboard: NodeClipboard | null) => void;
  clearNodeClipboard: () => void;
  /** Copies or moves a node selection into another project. */
  transferNodes: (request: NodeTransferRequest) => Promise<NodeTransferResult>;
  restoreTrash: (file: string) => Promise<void>;
}

export interface NodeClipboard {
  /** Project the ids belong to. */
  project: string;
  ids: string[];
  mode: NodeTransferMode;
  /** Titles, so the paste affordance can say what it holds. */
  labels: string[];
}

export interface UndoSlice {
  /** Whether the newest entry can be reverted, for enabling the UI affordance. */
  canUndo: boolean;
  /** What Ctrl/⌘+Z would revert, in UI wording. */
  undoLabel: string | null;
  undoLast: () => Promise<boolean>;
  /** Whether an undone step is waiting to be applied again. */
  canRedo: boolean;
  /** What Ctrl/⌘+Shift+Z would re-apply, in UI wording. */
  redoLabel: string | null;
  redoLast: () => Promise<boolean>;
}

/**
 * Live collaboration [P2]: who else is in this project and what they hold
 * open. Courtesy state — the hard guard against overwrites is the `Rev` check.
 */
export interface CollabSlice {
  /** The open socket, or null before it connects. */
  live: LiveConnection | null;
  /** This browser's peer id, so it can leave itself out of "who else". */
  selfPeerID: string;
  peers: Peer[];
  locks: NodeLock[];
  /** Set when a lock request lost to someone already editing. */
  lockDenied: { nodeId: string; actor: string } | null;
  /**
   * Cards a peer is dragging right now, by their peer id. Display only: the
   * position that lasts arrives afterwards as a graph op.
   */
  ghosts: Record<string, DragGhost>;
  /**
   * Where each peer's caret and pointer are, by peer id. Ephemeral: nobody
   * reads it back after the person leaves.
   */
  presences: Record<string, PresenceState>;

  setLive: (live: LiveConnection | null) => void;
  setSelfPeerID: (id: string) => void;
  setPeers: (peers: Peer[]) => void;
  setLocks: (locks: NodeLock[]) => void;
  setLockDenied: (denied: { nodeId: string; actor: string } | null) => void;
  setGhost: (peerId: string, ghost: DragGhost | null) => void;
  setPresence: (peerId: string, state: PresenceState | null) => void;

  reportPresence: (nodeId: string, editing: boolean) => void;
  requestLock: (nodeId: string, steal?: boolean) => void;
  releaseLock: (nodeId: string) => void;
  /** Tells the room where this browser's dragged selection currently sits. */
  reportDrag: (ids: string[], dx: number, dy: number, active: boolean) => void;
  /**
   * Publishes this browser's caret and pointer. The state is merged with what
   * was last sent, so the editor and the board can each report their own half.
   */
  reportPresenceState: (patch: PresenceState) => void;
}

/** What a config save sends. Everything but the backend itself is optional. */
export interface RemoteConfigInput {
  kind: RemoteKind;
  folder?: string;
  driveFolderId?: string;
  autoBackup?: boolean;
  intervalHours?: number;
  /** -1 keeps every bundle forever; otherwise the number kept. */
  retainBundles?: number;
}

/**
 * Remote backup and the settings next to it [A-05]. Server state, shared by
 * every dialog that shows it, so that two of them open at once cannot disagree
 * about what is configured.
 */
export interface RemoteSlice {
  /** `/api/remote/config` as the server last reported it; null before the first read. */
  remoteConfig: RemoteConfig | null;
  /** What the configured backend holds. null means "not read", [] means "none". */
  remoteBundles: RemoteBundle[] | null;
  /** How the workspace compares to its newest cloud snapshot. */
  remoteSyncStatus: RemoteSyncStatus | null;
  driveCredentials: DriveCredentialStatus | null;
  notifySettings: NotifySettings | null;
  /** The server never returns the stored SMTP password, only whether it has one. */
  notifyHasPassword: boolean;

  refreshRemoteConfig: () => Promise<void>;
  refreshRemoteBundles: () => Promise<void>;
  refreshRemoteSyncStatus: () => Promise<void>;
  refreshDriveCredentials: () => Promise<void>;
  refreshNotifySettings: () => Promise<void>;

  /** Resolves with the *effective* config: the server clamps what it must. */
  saveRemoteConfig: (config: RemoteConfigInput) => Promise<CommandResult<RemoteConfig>>;
  /** Pushes one project (the active one if omitted) as a bundle. */
  pushRemoteBundle: (project?: string) => Promise<CommandResult<RemoteBundle>>;
  /** Pushes the whole workspace, the way the scheduler does. */
  flushRemoteSync: () => Promise<CommandResult>;
  /** Installs a bundle as a new project and switches to it; resolves with its name. */
  importRemoteBundle: (bundleId: string) => Promise<CommandResult<string>>;
  connectDriveWorkspace: (
    folderId: string,
    bundleId: string,
    name?: string,
  ) => Promise<CommandResult<string>>;
  saveDriveCredentials: (
    clientId: string,
    clientSecret: string,
  ) => Promise<CommandResult>;
  clearDriveCredentials: () => Promise<CommandResult>;
  disconnectDrive: () => Promise<CommandResult>;
  saveNotifySettings: (settings: NotifySettings) => Promise<CommandResult>;
  /** Saves, then asks the server to send one mail to the default recipient. */
  sendNotifyTest: (settings: NotifySettings) => Promise<CommandResult>;
}

/**
 * Where node files sit inside nodes/ [B-06]. Purely an explorer concern: the
 * graph never refers to a folder, only to a node id.
 */
export interface NodeFolderSlice {
  /** Every folder path under nodes/, sorted so parents come first. */
  nodeFolders: string[];
  /** Node id → its folder path. Nodes at the root are absent. */
  nodeFolderOf: Record<string, string>;

  refreshNodeFolders: () => Promise<void>;
  createNodeFolder: (path: string) => Promise<void>;
  renameNodeFolder: (path: string, name: string) => Promise<void>;
  moveNodeFolder: (path: string, parent: string) => Promise<void>;
  /** Removes the folder; whatever it held moves up to the parent folder. */
  deleteNodeFolder: (path: string) => Promise<void>;
  moveNodesToFolder: (ids: string[], folder: string) => Promise<void>;
}

export type AppState = OperationsSlice &
  PreferencesSlice &
  WorkspaceSlice &
  DocumentSlice &
  RunSlice &
  GraphSlice &
  CollabSlice &
  NodeFolderSlice &
  RemoteSlice &
  UndoSlice;

/** Slice creator bound to the composed store, so slices can call each other. */
export type AppSlice<T> = StateCreator<AppState, [], [], T>;
