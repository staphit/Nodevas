import { useCallback, useEffect, useState } from "react";
import type { RemoteSyncState } from "../../api";
import { remoteScope, useApp, useOperationPending } from "../../store";
import { reason } from "./format";

const attentionStates = new Set<RemoteSyncState>([
  "local-newer",
  "remote-newer",
  "conflict",
  "remote-missing",
]);

function stateCopy(state: RemoteSyncState): { title: string; detail: string } {
  switch (state) {
    case "local-newer":
      return {
        title: "本機版本較新",
        detail: "雲端尚未包含目前修改，可立即上傳新的快照。",
      };
    case "remote-newer":
      return {
        title: "雲端版本較新",
        detail: "請先查看雲端快照，再決定是否匯入；Nodevas 不會自動覆蓋本機。",
      };
    case "conflict":
      return {
        title: "本機與雲端都有修改",
        detail: "這是同步衝突。請在版本中心選擇保留哪一邊。",
      };
    case "remote-missing":
      return {
        title: "尚未建立雲端快照",
        detail: "目前本機有內容，可建立第一份雲端版本。",
      };
    default:
      return { title: "雲端同步", detail: "" };
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
  const status = useApp((state) => state.remoteSyncStatus);
  const refresh = useApp((state) => state.refreshRemoteSyncStatus);
  const flushRemoteSync = useApp((state) => state.flushRemoteSync);
  const busy = useOperationPending(remoteScope.bundle());
  const [error, setError] = useState("");

  const reload = useCallback(() => {
    void refresh().then(
      () => setError(""),
      (failure: unknown) => setError(reason(failure, "無法取得同步狀態")),
    );
  }, [refresh]);

  useEffect(reload, [reload]);

  const flush = async () => {
    setError("");
    const result = await flushRemoteSync();
    if (!result.ok) setError(result.message);
  };

  if (!status || (!attentionStates.has(status.state) && !error)) return null;
  const copy = stateCopy(status.state);
  const canFlush = status.state === "local-newer" || status.state === "remote-missing";

  return (
    <div className={`remote-sync-banner ${status.state}`} role="status">
      <div>
        <strong>{error || copy.title}</strong>
        <span>{error ? "請稍後重試，或開啟雲端備份中心檢查版本。" : copy.detail}</span>
      </div>
      <div className="remote-sync-actions">
        {canFlush && (
          <button type="button" disabled={busy} onClick={() => void flush()}>
            {busy ? "上傳中…" : "立即上傳"}
          </button>
        )}
        <button type="button" disabled={busy} onClick={onOpenBackup}>
          查看版本
        </button>
        <button type="button" className="remote-sync-refresh" disabled={busy} onClick={reload}>
          重查
        </button>
      </div>
    </div>
  );
}
