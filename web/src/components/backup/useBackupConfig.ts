/**
 * The backup tab's editing state.
 *
 * The server's config lives in the store; what sits here is the *draft* of it —
 * the values in the form between opening the dialog and pressing 儲存設定. The
 * two are deliberately separate: 「備份整個工作區」 must be refused while the
 * form disagrees with what the server would use, and that question can only be
 * asked by something that holds both.
 */

import { useCallback, useEffect, useState } from "react";
import type { RemoteBundle, RemoteConfig, RemoteKind } from "../../api";
import { useI18n } from "../../i18n";
import { remoteScope, useApp, useOperationPending } from "../../store";
import { reason } from "./format";

export type Note = { text: string; kind: "ok" | "error" } | null;

/** Matches the server's default and floor (internal/remote/backup.go). */
export const DEFAULT_RETAIN = 14;
export const MIN_RETAIN = 3;

export interface BackupDraft {
  kind: RemoteKind;
  folder: string;
  driveFolderId: string;
  autoBackup: boolean;
  intervalHours: number;
  /** Unticked is the server's -1: keep everything, delete nothing. */
  pruneOld: boolean;
  retainBundles: number;
}

const EMPTY_DRAFT: BackupDraft = {
  kind: "",
  folder: "",
  driveFolderId: "",
  autoBackup: false,
  intervalHours: 24,
  pruneOld: true,
  retainBundles: DEFAULT_RETAIN,
};

export function draftFromConfig(config: RemoteConfig): BackupDraft {
  return {
    // A finished OAuth round trip leaves Drive connected but not yet chosen as
    // the backend. Preselecting it — and letting that read as unsaved — is the
    // rest of the sentence the user started.
    kind: config.kind || (config.driveConnected ? "drive" : ""),
    folder: config.folder,
    driveFolderId: config.driveFolderId,
    autoBackup: config.autoBackup,
    intervalHours: config.intervalHours > 0 ? config.intervalHours : 24,
    // A negative count is the server's "never delete anything"; keep the last
    // real number in the box so unticking and re-ticking does not silently
    // change the policy.
    pruneOld: config.retainBundles >= 0,
    retainBundles: config.retainBundles > 0 ? config.retainBundles : DEFAULT_RETAIN,
  };
}

/** What the draft would send as `retainBundles`. */
export function retentionOf(draft: BackupDraft): number {
  return draft.pruneOld ? draft.retainBundles : -1;
}

export function draftIsDirty(draft: BackupDraft, config: RemoteConfig): boolean {
  return (
    draft.kind !== config.kind ||
    draft.folder !== config.folder ||
    draft.driveFolderId !== config.driveFolderId ||
    draft.autoBackup !== config.autoBackup ||
    draft.intervalHours !== (config.intervalHours > 0 ? config.intervalHours : 24) ||
    retentionOf(draft) !== config.retainBundles
  );
}

export function useBackupConfig() {
  const { t } = useI18n();
  const config = useApp((state) => state.remoteConfig);
  const bundles = useApp((state) => state.remoteBundles);
  const activeProject = useApp((state) => state.activeProject);
  const refreshRemoteConfig = useApp((state) => state.refreshRemoteConfig);
  const refreshRemoteBundles = useApp((state) => state.refreshRemoteBundles);
  const saveRemoteConfig = useApp((state) => state.saveRemoteConfig);
  const pushRemoteBundle = useApp((state) => state.pushRemoteBundle);
  const flushRemoteSync = useApp((state) => state.flushRemoteSync);
  const importRemoteBundle = useApp((state) => state.importRemoteBundle);

  const [draft, setDraft] = useState<BackupDraft>(EMPTY_DRAFT);
  const [note, setNote] = useState<Note>(null);

  // Three lanes, one disabled state: nothing in this dialog is safe to press
  // while any of them is mid-flight [A-02].
  const configBusy = useOperationPending(remoteScope.config());
  const bundleBusy = useOperationPending(remoteScope.bundle());
  const driveBusy = useOperationPending(remoteScope.drive());
  const busy = configBusy || bundleBusy || driveBusy;

  useEffect(() => {
    void refreshRemoteConfig().catch((error: unknown) =>
      setNote({ text: reason(error, t("backup.loadConfigFailed")), kind: "error" }),
    );
  }, [refreshRemoteConfig, t]);

  // Nothing has been pushed anywhere until a backend is chosen, so the listing
  // is only worth a request once one is — or once Drive is connected and the
  // form is about to preselect it.
  const configured = Boolean(config && (config.kind || config.driveConnected));
  useEffect(() => {
    if (!configured) return;
    void refreshRemoteBundles().catch((error: unknown) =>
      setNote({ text: reason(error, t("backup.loadBundlesFailed")), kind: "error" }),
    );
  }, [configured, refreshRemoteBundles, t]);

  // Adopting the server's reply is also how a clamped retention count reaches
  // the form. Edits are never lost to this: every action that re-reads the
  // config is disabled while the draft is dirty.
  useEffect(() => {
    if (config) setDraft(draftFromConfig(config));
  }, [config]);

  const patch = useCallback((partial: Partial<BackupDraft>) => {
    setDraft((current) => ({ ...current, ...partial }));
  }, []);

  const save = useCallback(async () => {
    setNote(null);
    const result = await saveRemoteConfig({
      kind: draft.kind,
      folder: draft.folder,
      driveFolderId: draft.driveFolderId,
      autoBackup: draft.autoBackup,
      intervalHours: draft.intervalHours,
      retainBundles: retentionOf(draft),
    });
    setNote(
      result.ok
        ? { text: t("backup.settingsSaved"), kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  }, [draft, saveRemoteConfig, t]);

  const push = useCallback(async () => {
    setNote(null);
    const result = await pushRemoteBundle(activeProject || undefined);
    setNote(
      result.ok
        ? { text: t("backup.projectBackedUp", { name: result.value.name }), kind: "ok" }
        : { text: result.message, kind: "error" },
    );
  }, [activeProject, pushRemoteBundle, t]);

  const pushWorkspace = useCallback(async () => {
    setNote(null);
    const result = await flushRemoteSync();
    if (result.ok) {
      setNote({ text: t("backup.workspaceBackedUp"), kind: "ok" });
      return;
    }
    // A conflict means the newest remote snapshot came from somewhere else.
    // That refusal is the sync model working, so it gets directions rather
    // than a retry.
    const conflicted = /conflict|remote snapshot changed/i.test(result.message);
    setNote({
      text: conflicted
        ? t("backup.conflictHint")
        : result.message,
      kind: "error",
    });
  }, [flushRemoteSync, t]);

  const restore = useCallback(
    async (bundle: RemoteBundle) => {
      setNote(null);
      const result = await importRemoteBundle(bundle.id);
      setNote(
        result.ok
          ? { text: t("backup.restoredAs", { name: result.value }), kind: "ok" }
          : { text: result.message, kind: "error" },
      );
    },
    [importRemoteBundle, t],
  );

  const reloadBundles = useCallback(() => {
    void refreshRemoteBundles().catch((error: unknown) =>
      setNote({ text: reason(error, t("backup.loadBundlesFailed")), kind: "error" }),
    );
  }, [refreshRemoteBundles, t]);

  return {
    config,
    bundles,
    draft,
    patch,
    note,
    setNote,
    busy,
    dirty: config != null && draftIsDirty(draft, config),
    canBackup: config != null && config.kind !== "",
    save,
    push,
    pushWorkspace,
    restore,
    reloadBundles,
  };
}

export type BackupConfigState = ReturnType<typeof useBackupConfig>;
