/**
 * Remote backup slice [A-05].
 *
 * Everything behind `/api/remote/*`: where bundles are pushed, the Google Drive
 * credentials and token, the bundle listing, and how the workspace compares to
 * its newest cloud snapshot. The deadline-mail settings sit here too — they are
 * the same kind of thing, workspace-wide server config that a modal edits, and
 * they are the only other one.
 *
 * This is a slice because the config is *server* state read by four dialogs.
 * While each kept a `useState` copy of it, two of them on screen at once showed
 * — and wrote — divergent versions of one file: a push from the backup dialog
 * left the sync banner claiming the local copy was still newer, and saving the
 * backend from one modal was invisible to the other until it was reopened. One
 * config on disk, one copy of it here.
 *
 * Folder browsing deliberately stays in the components. A Drive folder stack is
 * where one picker has navigated to, not something the server holds, and two
 * pickers open at once are *supposed* to sit in different folders.
 */

import {
  api,
  type NotifySettings,
  type RemoteBundle,
  type RemoteConfig,
} from "../api";
import { coalesceRequest } from "./coalesce";
import {
  commandFailed,
  commandSucceeded,
  describeFailure,
  type CommandResult,
  type OperationScope,
} from "./operations";
import type { AppSlice, RemoteConfigInput, RemoteSlice } from "./types";

/**
 * Backup work runs in lanes that are genuinely independent — editing the
 * destination, moving a bundle, authorising Drive, wiring up mail — so each
 * reports on its own badge rather than sharing a single "remote" one.
 */
export const remoteScope = {
  config: (): OperationScope => "remote:config",
  bundle: (): OperationScope => "remote:bundle",
  drive: (): OperationScope => "remote:drive",
  notify: (): OperationScope => "remote:notify",
} as const;

export const createRemoteSlice: AppSlice<RemoteSlice> = (set, get) => {
  /**
   * One mutation, in the operation vocabulary [A-02]. Failure comes back by
   * value so a panel can print it next to the control that caused it instead of
   * throwing past it.
   */
  const command = async <T>(
    scope: OperationScope,
    name: string,
    fallback: string,
    body: () => Promise<T>,
  ): Promise<CommandResult<T>> => {
    get().beginOperation(scope, name);
    try {
      const value = await body();
      get().settleOperation(scope, { status: "saved", operation: name });
      return commandSucceeded(name, scope, value);
    } catch (e) {
      const message = describeFailure(e, fallback);
      get().settleOperation(scope, { status: "error", operation: name, message });
      return commandFailed<T>(name, scope, message);
    }
  };

  /**
   * Re-reads after a write. A refresh that fails must not turn a push that
   * succeeded into an error — the bundle is on the remote either way, and the
   * next read heals the display.
   */
  const reread = (...refresh: Array<() => Promise<void>>) =>
    Promise.all(refresh.map((load) => load().catch(() => undefined)));

  return {
    remoteConfig: null,
    remoteBundles: null,
    remoteSyncStatus: null,
    driveCredentials: null,
    notifySettings: null,
    notifyHasPassword: false,

    // Opening the backup dialog, saving the destination and the push that
    // follows all want the config back; inside one interaction that is one read.
    refreshRemoteConfig: () =>
      coalesceRequest("remote-config", async () => {
        set({ remoteConfig: await api.getRemoteConfig() });
      }),

    refreshRemoteBundles: () =>
      coalesceRequest("remote-bundles", async () => {
        const result = await api.listRemoteBundles();
        set({ remoteBundles: result.bundles });
      }),

    refreshRemoteSyncStatus: () =>
      coalesceRequest("remote-sync", async () => {
        set({ remoteSyncStatus: await api.getRemoteSyncStatus() });
      }),

    refreshDriveCredentials: () =>
      coalesceRequest("drive-credentials", async () => {
        set({ driveCredentials: await api.getDriveCredentials() });
      }),

    refreshNotifySettings: () =>
      coalesceRequest("notify-settings", async () => {
        const response = await api.getNotifySettings();
        set({
          notifySettings: response.settings,
          notifyHasPassword: response.hasPassword,
        });
      }),

    // The server returns the effective config, not the one that was sent: a
    // retention count below its floor comes back clamped up. Adopting the reply
    // is what shows the user the number that will actually be used.
    saveRemoteConfig: (config: RemoteConfigInput) =>
      command(remoteScope.config(), "remote.saveConfig", "儲存備份設定失敗", async () => {
        const next = await api.putRemoteConfig(config);
        set({ remoteConfig: next, ...(next.kind ? {} : { remoteBundles: null }) });
        if (next.kind) await reread(get().refreshRemoteBundles, get().refreshRemoteSyncStatus);
        return next;
      }),

    pushRemoteBundle: (project) =>
      command(remoteScope.bundle(), "remote.push", "備份失敗", async () => {
        const result = await api.pushRemote(project);
        await reread(
          get().refreshRemoteConfig,
          get().refreshRemoteBundles,
          get().refreshRemoteSyncStatus,
        );
        return result.bundle;
      }),

    // The workspace flush the scheduler runs, on demand. It refuses with a
    // conflict when the newest remote snapshot came from somewhere else; that
    // refusal is the sync model working, so it is reported and not retried.
    flushRemoteSync: () =>
      command(remoteScope.bundle(), "remote.flush", "備份失敗", async () => {
        await api.flushRemoteSync();
        await reread(
          get().refreshRemoteConfig,
          get().refreshRemoteBundles,
          get().refreshRemoteSyncStatus,
        );
      }),

    // A restore never overwrites: the bundle lands as a new project, and the
    // user is taken to it so the result is visible rather than claimed.
    importRemoteBundle: (bundleId) =>
      command(remoteScope.bundle(), "remote.import", "還原失敗", async () => {
        const result = await api.importRemote(bundleId);
        await get().refreshProjects();
        await get().switchProject(result.active);
        return result.active;
      }),

    connectDriveWorkspace: (folderId, bundleId, name) =>
      command(
        remoteScope.drive(),
        "remote.connectDrive",
        "從 Google Drive 匯入失敗",
        async () => {
          const result = await api.connectDriveWorkspace(folderId, bundleId, name);
          await get().refreshProjects();
          await get().switchProject(result.active);
          await reread(get().refreshRemoteConfig, get().refreshRemoteSyncStatus);
          return result.active;
        },
      ),

    // Credentials decide `driveAvailable`, which the backup dialog reads off the
    // config, so the config is stale the moment they change.
    saveDriveCredentials: (clientId, clientSecret) =>
      command(remoteScope.drive(), "remote.saveDriveCredentials", "儲存 OAuth 憑證失敗", async () => {
        set({ driveCredentials: await api.putDriveCredentials(clientId, clientSecret) });
        await reread(get().refreshRemoteConfig);
      }),

    clearDriveCredentials: () =>
      command(remoteScope.drive(), "remote.clearDriveCredentials", "清除 OAuth 憑證失敗", async () => {
        set({ driveCredentials: await api.deleteDriveCredentials() });
        await reread(get().refreshRemoteConfig);
      }),

    disconnectDrive: () =>
      command(remoteScope.drive(), "remote.disconnectDrive", "中斷連線失敗", async () => {
        await api.disconnectDrive();
        await reread(get().refreshRemoteConfig);
      }),

    saveNotifySettings: (settings: NotifySettings) =>
      command(remoteScope.notify(), "notify.save", "儲存失敗", async () => {
        await api.putNotifySettings(settings);
        // An empty password field means "keep the stored one", so saving a
        // filled-in field is the moment one starts existing.
        set({
          notifySettings: settings,
          notifyHasPassword: get().notifyHasPassword || settings.smtpPass !== "",
        });
      }),

    sendNotifyTest: (settings: NotifySettings) =>
      command(remoteScope.notify(), "notify.test", "測試寄送失敗", async () => {
        // Persist first, so the server tests exactly what the user is looking
        // at. A save that was refused must stop here: the mail would otherwise
        // go out through the transport the user just failed to change.
        const saved = await get().saveNotifySettings(settings);
        if (!saved.ok) throw new Error(saved.message);
        await api.testNotify(settings.defaultTo);
      }),
  };
};

/** RFC3339's zero value is the server's "never"; it must not reach a formatter. */
export function hasEverBackedUp(config: RemoteConfig | null): boolean {
  return config != null && !config.lastBackupAt.startsWith("0001-01-01");
}

/** True when Drive is wired up far enough to list folders and push. */
export function driveReady(config: RemoteConfig | null): boolean {
  return (
    config != null &&
    config.driveAvailable &&
    config.driveConnected &&
    !config.driveNeedsReauth
  );
}

/** Newest first, whatever order the backend happened to list them in. */
export function sortBundles(bundles: RemoteBundle[]): RemoteBundle[] {
  return [...bundles].sort((a, b) => b.modified.localeCompare(a.modified));
}
