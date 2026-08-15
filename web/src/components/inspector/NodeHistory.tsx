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
import { formatLocalizedDateTime, localizedStatusLabel, useI18n } from "../../i18n";

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
  const { t, language } = useI18n();
  const runState = useApp((s) => s.runState);
  const graph = useApp((s) => s.graph);
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
      setViewError(error instanceof Error ? error.message : t("history.binaryRestore"));
    }
  };

  const restore = async (version: string) => {
    const confirmed = await confirmAction({
      title: t("history.restoreTitle"),
      description: t("history.restoreDescription", { path, version }),
      confirmLabel: t("history.restoreVersion"),
    });
    if (!confirmed) return;
    await api.restoreHistory(path, version);
    await onRestored();
    setViewing(null);
  };

  return (
    <details className="node-history" open={open} onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}>
      <summary>
        {t("history.title")}{events.length > 0 ? ` (${t("history.statusRecords", { count: String(events.length) })})` : ""}
      </summary>
      {events.length > 0 && (
        <ul className="nh-events">
          {events.map((ev, i) => (
            <li key={i}>
                <span className="mono nh-time">{formatLocalizedDateTime(ev.t, language)}</span>
              <span className="nh-transition">
                {ev.from ? localizedStatusLabel(ev.from, graph?.ui?.customStatuses ?? []) : "—"} → <b>{ev.to ? localizedStatusLabel(ev.to, graph?.ui?.customStatuses ?? []) : t("history.unknownStatus")}</b>
              </span>
              {ev.by && <span className="nh-by">by {ev.by}</span>}
              {ev.note && <span className="nh-note">「{ev.note}」</span>}
            </li>
          ))}
        </ul>
      )}
      <div className="nh-files">
        <b>{t("history.files")}</b>
        {versions.length === 0 ? (
          <span className="nh-none">{t("history.none")}</span>
        ) : (
          <ul>
            {versions.map((v) => (
              <li key={v.name}>
                <span className="mono nh-time">{formatLocalizedDateTime(v.at, language)}</span>
                <span className="nh-size">{v.size}B</span>
                <button
                  type="button"
                  className={viewing?.name === v.name ? "on" : ""}
                  aria-pressed={viewing?.name === v.name}
                  onClick={() => void preview(v.name, v.at)}
                  disabled={!previewable}
                  title={
                    previewable
                      ? t("history.previewBeforeRestore")
                      : t("history.binaryRestore")
                  }
                >
                  <IconEye size={12} />
                  {t("history.preview")}
                </button>
                <button type="button" onClick={() => void restore(v.name).catch(reportError)}>
                  {t("history.restore")}
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
            <div className="nh-preview" role="region" aria-label={t("history.versionPreview")}>
            <div className="nh-preview-head">
              <b>{t("history.contentAt", { date: formatLocalizedDateTime(viewing.at, language) })}</b>
              <span className="nh-preview-hint">{t("history.readOnlyHint")}</span>
              <button
                type="button"
                onClick={() => void restore(viewing.name).catch(reportError)}
              >
                {t("history.restoreThis")}
              </button>
              <button type="button" onClick={() => setViewing(null)} aria-label={t("history.closePreview")}>
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
