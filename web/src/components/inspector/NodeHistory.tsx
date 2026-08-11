/**
 * Node history [B-06].
 *
 * Two different records side by side: journal status events (what happened) and
 * file snapshots taken before each save (what the document looked like).
 */

import { useEffect, useMemo, useState } from "react";
import { api, type PageFormat } from "../../api";
import { renderMarkdown, sanitizeHTML } from "../../markdown";
import { reportError, useApp } from "../../store";
import { confirmAction } from "../ConfirmDialog";
import { IconEye } from "../../icons";

export /** per-node change log: lifecycle stamps + file version snapshots */
function NodeHistory({
  id,
  path,
  format,
  onRestored,
}: {
  id: string;
  /** Project-relative file the versions belong to: the node's main document
   * or whichever subpage is open. */
  path: string;
  format: PageFormat;
  onRestored: () => void | Promise<void>;
}) {
  const runState = useApp((s) => s.runState);
  const [versions, setVersions] = useState<{ name: string; at: string; size: number }[]>([]);
  const [open, setOpen] = useState(false);
  /** Snapshot being looked at before deciding whether to restore it. */
  const [viewing, setViewing] = useState<{
    name: string;
    at: string;
    content: string;
  } | null>(null);
  const [viewError, setViewError] = useState<string | null>(null);

  const events = useMemo(
    () =>
      (runState?.history ?? [])
        .filter((event) => event.node === id && event.event === "status")
        .slice()
        .reverse(),
    [runState, id],
  );

  // Switching page switches the file: the previous file's versions and any
  // open preview no longer describe what is on screen.
  useEffect(() => {
    setVersions([]);
    setViewing(null);
    setViewError(null);
  }, [path]);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .getHistory(path)
      .then((r) => {
        if (!cancelled) setVersions(r.versions);
      })
      .catch(() => {
        if (!cancelled) setVersions([]);
      });
    return () => {
      cancelled = true;
    };
  }, [open, path]);

  // A .docx is a zip; there is no text to show, so the choice is restore or
  // nothing.
  const previewable = format !== "docx";

  const preview = async (version: string, at: string) => {
    setViewError(null);
    try {
      const snapshot = await api.getHistoryVersion(path, version);
      setViewing({ name: version, at, content: snapshot.content });
    } catch (error) {
      setViewError(error instanceof Error ? error.message : "無法讀取此版本");
    }
  };

  const restore = async (version: string) => {
    const confirmed = await confirmAction({
      title: "還原歷史版本",
      description: `將 ${path} 還原到 ${version}。目前版本會先建立快照，可再復原。`,
      confirmLabel: "還原版本",
    });
    if (!confirmed) return;
    await api.restoreHistory(path, version);
    await onRestored();
    setViewing(null);
  };

  return (
    <details className="node-history" open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
      <summary>
        歷程{events.length > 0 ? `(${events.length} 筆狀態紀錄)` : ""}
      </summary>
      {events.length > 0 && (
        <ul className="nh-events">
          {events.map((ev, i) => (
            <li key={i}>
              <span className="mono nh-time">{new Date(ev.t).toLocaleString()}</span>
              <span className="nh-transition">
                {ev.from || "無"} → <b>{ev.to}</b>
              </span>
              {ev.by && <span className="nh-by">by {ev.by}</span>}
              {ev.note && <span className="nh-note">「{ev.note}」</span>}
            </li>
          ))}
        </ul>
      )}
      <div className="nh-files">
        <b>檔案版本</b>
        {versions.length === 0 ? (
          <span className="nh-none">尚無快照(存檔時自動產生)</span>
        ) : (
          <ul>
            {versions.map((v) => (
              <li key={v.name}>
                <span className="mono nh-time">{new Date(v.at).toLocaleString()}</span>
                <span className="nh-size">{v.size}B</span>
                <button
                  type="button"
                  className={viewing?.name === v.name ? "on" : ""}
                  aria-pressed={viewing?.name === v.name}
                  onClick={() => void preview(v.name, v.at)}
                  disabled={!previewable}
                  title={
                    previewable
                      ? "先看內容再決定要不要還原"
                      : "此格式是二進位檔，只能直接還原"
                  }
                >
                  <IconEye size={12} />
                  預覽
                </button>
                <button type="button" onClick={() => void restore(v.name).catch(reportError)}>
                  還原
                </button>
              </li>
            ))}
          </ul>
        )}
        {viewError && (
          <div className="nh-preview-error" role="alert">
            {viewError}
          </div>
        )}
        {viewing && (
          <div className="nh-preview" role="region" aria-label="版本預覽">
            <div className="nh-preview-head">
              <b>{new Date(viewing.at).toLocaleString()} 的內容</b>
              <span className="nh-preview-hint">唯讀；還原前目前版本會先建立快照</span>
              <button
                type="button"
                onClick={() => void restore(viewing.name).catch(reportError)}
              >
                還原這一版
              </button>
              <button type="button" onClick={() => setViewing(null)} aria-label="關閉預覽">
                ×
              </button>
            </div>
            {/* Plain text stays plain: md-preview already styles <pre>, so no
                new CSS is needed for it. */}
            {format === "txt" ? (
              <div className="nh-preview-body md-preview">
                <pre>{viewing.content}</pre>
              </div>
            ) : (
              <div
                className="nh-preview-body md-preview"
                dangerouslySetInnerHTML={{
                  __html:
                    format === "html"
                      ? sanitizeHTML(viewing.content)
                      : renderMarkdown(viewing.content),
                }}
              />
            )}
          </div>
        )}
      </div>
    </details>
  );
}
