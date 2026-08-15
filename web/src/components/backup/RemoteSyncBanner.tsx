import { useCallback, useEffect, useState } from "react";
import type { RemoteSyncState } from "../../api";
import { useI18n } from "../../i18n";
import { remoteScope, useApp, useOperationPending } from "../../store";
import { reason } from "./format";

const attentionStates = new Set<RemoteSyncState>([
  "local-newer",
  "remote-newer",
  "conflict",
  "remote-missing",
]);

function stateCopy(
  state: RemoteSyncState,
  t: (key: string) => string,
): { title: string; detail: string } {
  switch (state) {
    case "local-newer":
      return {
        title: t("backup.syncLocalNewer"),
        detail: t("backup.syncLocalNewerDetail"),
      };
    case "remote-newer":
      return {
        title: t("backup.syncRemoteNewer"),
        detail: t("backup.syncRemoteNewerDetail"),
      };
    case "conflict":
      return {
        title: t("backup.syncConflict"),
        detail: t("backup.syncConflictDetail"),
      };
    case "remote-missing":
      return {
        title: t("backup.syncRemoteMissing"),
        detail: t("backup.syncRemoteMissingDetail"),
      };
    default:
      return { title: t("backup.syncTitle"), detail: "" };
  }
}

/**
 * The one line that says the cloud copy and this machine have drifted apart.
 *
 * It reads the status from the store, so every push made anywhere in the app
 * updates it: the write itself re-reads, and this only has to render what came
 * back. It used to listen for a window event the backup dialog dispatched by
 * hand, which meant a push made by anything else left this banner lying.
 */
export function RemoteSyncBanner({ onOpenBackup }: { onOpenBackup: () => void }) {
  const { t } = useI18n();
  const status = useApp((state) => state.remoteSyncStatus);
  const refresh = useApp((state) => state.refreshRemoteSyncStatus);
  const flushRemoteSync = useApp((state) => state.flushRemoteSync);
  const busy = useOperationPending(remoteScope.bundle());
  const [error, setError] = useState("");

  const reload = useCallback(() => {
    void refresh().then(
      () => setError(""),
      (failure: unknown) => setError(reason(failure, t("backup.syncStatusError"))),
    );
  }, [refresh, t]);

  useEffect(reload, [reload]);

  const flush = async () => {
    setError("");
    const result = await flushRemoteSync();
    if (!result.ok) setError(result.message);
  };

  if (!status || (!attentionStates.has(status.state) && !error)) return null;
  const copy = stateCopy(status.state, t);
  const canFlush = status.state === "local-newer" || status.state === "remote-missing";

  return (
    <div className={`remote-sync-banner ${status.state}`} role="status">
      <div>
        <strong>{error || copy.title}</strong>
        <span>{error ? t("backup.syncRetryHint") : copy.detail}</span>
      </div>
      <div className="remote-sync-actions">
        {canFlush && (
          <button type="button" disabled={busy} onClick={() => void flush()}>
            {busy ? t("backup.uploading") : t("backup.uploadNow")}
          </button>
        )}
        <button type="button" disabled={busy} onClick={onOpenBackup}>
          {t("backup.viewVersions")}
        </button>
        <button type="button" className="remote-sync-refresh" disabled={busy} onClick={reload}>
          {t("backup.recheck")}
        </button>
      </div>
    </div>
  );
}
