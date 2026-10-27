import { req } from "./http";

/**
 * Which off-box backend a workspace backs up to: a folder of project bundles,
 * or a git remote that gets each project's own history as a branch.
 */
export type RemoteKind = "" | "folder" | "git";

export interface RemoteConfig {
  kind: RemoteKind;
  folder: string;
  /** Push the whole workspace on a schedule. */
  autoBackup: boolean;
  /** Hours between scheduled pushes; 0 means the server default (24h). */
  intervalHours: number;
  /**
   * How many of this app's own bundles survive a scheduled push. The server
   * returns the effective value (a too-small setting is clamped up to its
   * floor), or -1 when pruning is turned off and nothing is ever deleted.
   */
  retainBundles: number;
  /** RFC3339; the zero value ("0001-01-01T00:00:00Z") means never. */
  lastBackupAt: string;
  /** The git backend's https:// repository. */
  gitUrl?: string;
  /** Sent with the token; empty means the server's default (x-access-token). */
  gitUser?: string;
  /** The server never returns the token, only whether one is stored. */
  hasGitToken?: boolean;
}

/** One bundle the remote holds. */
export interface RemoteBundle {
  id: string;
  name: string;
  size: number;
  modified: string;
  hash?: string;
}

export type RemoteSyncState =
  | "disabled"
  | "synced"
  | "local-newer"
  | "remote-newer"
  | "conflict"
  | "remote-missing";

/** How the last push of one project to the git remote went. */
export interface RemoteGitPush {
  at: string;
  branch: string;
  commit?: string;
  ok: boolean;
  error?: string;
}

/** One project's branch on the git remote. */
export interface RemoteGitProject {
  project: string;
  branch?: string;
  /** Absent when the state could not be worked out; error then says why. */
  state?: RemoteSyncState;
  localCommit?: string;
  remoteCommit?: string;
  error?: string;
  lastPush?: RemoteGitPush;
}

export interface RemoteSyncStatus {
  state: RemoteSyncState;
  localHash: string;
  lastPushedHash: string;
  lastPushedBundleId: string;
  remoteLatest: RemoteBundle | null;
  remoteHash: string;
  lastBackupAt: string;
  error?: string;
  /** The git backend's per-project view; `state` folds it into one answer. */
  projects?: RemoteGitProject[];
}

export const remoteApi = {
  getRemoteConfig: () => req<RemoteConfig>("/api/remote/config"),
  getRemoteSyncStatus: () => req<RemoteSyncStatus>("/api/remote/sync/status"),
  flushRemoteSync: () =>
    req<{ state: RemoteSyncState; bundle?: RemoteBundle }>(
      "/api/remote/sync/flush",
      { method: "POST" },
    ),
  putRemoteConfig: (config: {
    kind: RemoteKind;
    folder?: string;
    autoBackup?: boolean;
    intervalHours?: number;
    /** -1 keeps every bundle forever; otherwise the number kept. */
    retainBundles?: number;
    gitUrl?: string;
    gitUser?: string;
    /** Write-only. Empty keeps the stored token when the URL and user are unchanged. */
    gitToken?: string;
    clearGitToken?: boolean;
  }) =>
    req<RemoteConfig>("/api/remote/config", {
      method: "PUT",
      body: JSON.stringify(config),
    }),
  listRemoteBundles: () =>
    req<{ backend: RemoteKind; bundles: RemoteBundle[] }>("/api/remote/list"),
  /** Builds and uploads the export bundle for a project (active if omitted). */
  pushRemote: (project?: string) =>
    req<{ backend: RemoteKind; bundle: RemoteBundle }>(
      project
        ? `/api/remote/push?project=${encodeURIComponent(project)}`
        : "/api/remote/push",
      { method: "POST" },
    ),
  /** Pulls a bundle by id and installs it as a new project. */
  importRemote: (id: string, name?: string) =>
    req<{ active: string; root: string }>("/api/remote/import", {
      method: "POST",
      body: JSON.stringify({ id, ...(name?.trim() ? { name: name.trim() } : {}) }),
    }),
  /** ls-remote against the saved git remote, with the saved token. */
  testRemoteGit: () =>
    req<{ ok: boolean; branches: number }>("/api/remote/git/test", { method: "POST" }),
  /** Pushes a diverged project's history to a new side branch. */
  keepLocalRemoteGit: (project: string) =>
    req<{ branch: string }>(
      `/api/remote/git/keep-local?project=${encodeURIComponent(project)}`,
      { method: "POST" },
    ),
};
