import { useEffect, useMemo, useState } from "react";

import { IconClose, IconDoc } from "../icons";
import { nodeById, useApp } from "../store";
import { ConfirmDialogHost } from "./ConfirmDialog";
import { TabBody } from "./Drawer";

export function PopoutEditor() {
  const params = useMemo(() => new URLSearchParams(window.location.search), []);
  const nodeID = params.get("node") ?? "";
  const pageID = params.get("page") ?? "main";
  const graph = useApp((state) => state.graph);
  const theme = useApp((state) => state.theme);
  const loadAll = useApp((state) => state.loadAll);
  const refreshProjects = useApp((state) => state.refreshProjects);
  const refreshTrash = useApp((state) => state.refreshTrash);
  const openTab = useApp((state) => state.openTab);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editorDirty, setEditorDirty] = useState(false);
  const [saveRequest, setSaveRequest] = useState(0);

  const node = nodeById(graph, nodeID);
  const title = node?.title || nodeID || "節點";

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    if (!nodeID) {
      setError("缺少節點 ID，無法開啟彈窗。");
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    void Promise.all([loadAll(), refreshProjects(), refreshTrash()])
      .then(() => openTab(nodeID))
      .catch((loadError) => {
        if (!cancelled) {
          setError(
            loadError instanceof Error ? loadError.message : "無法開啟節點。",
          );
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [loadAll, nodeID, openTab, refreshProjects, refreshTrash]);

  useEffect(() => {
    document.title = `${title} — Nodevas`;
  }, [title]);

  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!editorDirty) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [editorDirty]);

  return (
    <main className="popout-editor-shell full-node-editor">
      <header className="popout-editor-head">
        <div className="popout-editor-title">
          <IconDoc size={16} />
          <div>
            <strong>{title}</strong>
            <span>{pageID === "main" ? "主頁" : pageID}</span>
          </div>
        </div>
        <div className="popout-editor-actions">
          <span className={editorDirty ? "unsaved" : ""}>
            {editorDirty ? "未儲存" : "已儲存"}
          </span>
          <button
            type="button"
            className="primary"
            disabled={!editorDirty}
            onClick={() => setSaveRequest((value) => value + 1)}
          >
            儲存 <kbd>Ctrl+S</kbd>
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label="關閉視窗"
            onClick={() => window.close()}
          >
            <IconClose size={16} />
          </button>
        </div>
      </header>

      {error && (
        <div className="banner banner-warn popout-banner">
          <span>{error}</span>
          <button type="button" onClick={() => setError(null)}>
            關閉
          </button>
        </div>
      )}

      {loading ? (
        <div className="popout-editor-loading">正在載入完整節點資訊…</div>
      ) : (
        <TabBody
          id={nodeID}
          initialPageID={pageID}
          saveRequest={saveRequest}
          onDirtyChange={setEditorDirty}
        />
      )}
      <ConfirmDialogHost />
    </main>
  );
}
