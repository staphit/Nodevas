/**
 * The Drive half of the backup tab: where bundles are filed, and whether this
 * app may still put them there.
 *
 * Which folder the picker is currently *looking at* stays here rather than in
 * the store — it is where one panel has navigated to, not something the server
 * holds, and the import tab browsing somewhere else at the same time is the
 * point, not a bug.
 */

import { useCallback, useState } from "react";
import { api, type DriveFolder } from "../../api";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import { reason } from "./format";

export const DRIVE_ROOT_LABEL = "雲端硬碟根目錄";

export function useDriveConnection({
  folderId,
  onPick,
  onError,
}: {
  /** The folder id the draft currently holds. */
  folderId: string;
  onPick: (folderId: string) => void;
  onError: (message: string) => void;
}) {
  const { t } = useI18n();
  const disconnectCommand = useApp((state) => state.disconnectDrive);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [folders, setFolders] = useState<DriveFolder[]>([]);
  const [stack, setStack] = useState<DriveFolder[]>([]);

  const list = useCallback(
    async (parent: string) => {
      setBusy(true);
      try {
        const result = await api.listDriveFolders(parent);
        setFolders(result.folders);
      } catch (error) {
        onError(reason(error, t("backup.driveFolderReadFailed")));
      } finally {
        setBusy(false);
      }
    },
    [onError, t],
  );

  const openPicker = useCallback(() => {
    setOpen(true);
    setStack([]);
    void list("root");
  }, [list]);

  // Entering a folder also chooses it: a bundle filed "in" a folder the user
  // only passed through would be a surprise, and there is no other Confirm.
  const enter = useCallback(
    (folder: DriveFolder) => {
      setStack((current) => [...current, folder]);
      onPick(folder.id);
      void list(folder.id);
    },
    [list, onPick],
  );

  const back = useCallback(() => {
    if (stack.length === 0) return;
    const next = stack.slice(0, -1);
    const parent = next.at(-1);
    setStack(next);
    onPick(parent?.id ?? "");
    void list(parent?.id ?? "root");
  }, [list, onPick, stack]);

  const toRoot = useCallback(() => {
    setStack([]);
    onPick("");
    void list("root");
  }, [list, onPick]);

  /** An id pasted into the box: no folder was walked to, so the trail is gone. */
  const selectFolderId = useCallback(
    (value: string) => {
      setStack([]);
      onPick(value.trim());
    },
    [onPick],
  );

  const disconnect = useCallback(() => disconnectCommand(), [disconnectCommand]);

  return {
    open,
    busy,
    folders,
    stack,
    /** What the current choice is called, in the picker and under the box. */
    label: stack.at(-1)?.name ?? (folderId || t("backup.driveRoot")),
    openPicker,
    enter,
    back,
    toRoot,
    selectFolderId,
    disconnect,
  };
}

export type DriveConnection = ReturnType<typeof useDriveConnection>;
