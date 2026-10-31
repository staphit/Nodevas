import { useEffect, useRef, useState } from "react";
import { analyticsApi, StaleSuggestionError, type Suggestion } from "../api/analytics";
import { useI18n } from "../i18n";
import { changeText, suggestionText } from "./analyticsFormat";

export interface SuggestionListProps {
  suggestions: Suggestion[];
  project?: string;
  /** Opens a Settings row (an open_settings change). */
  onOpenSettings?: (row: string) => void;
  /** Called after an apply or dismiss so the caller reloads. */
  onChanged?: () => void;
}

type Note = { key: string; kind: "applied" | "stale" | "error"; text?: string };

/** The confirm step: nothing is applied until the person presses Apply. */
function ConfirmApply({
  suggestion,
  onCancel,
  onConfirm,
  busy,
}: {
  suggestion: Suggestion;
  onCancel: () => void;
  onConfirm: () => void;
  busy: boolean;
}) {
  const { t } = useI18n();
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => cancelRef.current?.focus(), []);
  return (
    <div className="confirm-backdrop" onClick={onCancel}>
      <div
        className="confirm-dialog an-confirm"
        role="dialog"
        aria-modal="true"
        aria-label={t("analytics.applyConfirmTitle")}
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key === "Escape") onCancel();
        }}
      >
        <h3>{t("analytics.applyConfirmTitle")}</h3>
        {suggestion.change && <p className="an-change">{changeText(suggestion.change, t)}</p>}
        <div className="an-actions">
          <button ref={cancelRef} type="button" onClick={onCancel}>
            {t("common.cancel")}
          </button>
          <button type="button" className="primary" disabled={busy} onClick={onConfirm}>
            {t("analytics.applyConfirm")}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Suggestions, each applied only after the person confirms it. */
export default function SuggestionList({ suggestions, project, onOpenSettings, onChanged }: SuggestionListProps) {
  const { t } = useI18n();
  const [list, setList] = useState(suggestions);
  const [confirming, setConfirming] = useState<Suggestion | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note | null>(null);
  useEffect(() => setList(suggestions), [suggestions]);

  if (list.length === 0 && !note) return null;

  const apply = async (suggestion: Suggestion) => {
    if (!suggestion.change) return;
    setBusy(true);
    try {
      await analyticsApi.applySuggestion(suggestion.key, suggestion.change, project);
      setConfirming(null);
      setList((current) => current.filter((s) => s.key !== suggestion.key));
      setNote({ key: suggestion.key, kind: "applied" });
      onChanged?.();
    } catch (error) {
      setConfirming(null);
      if (error instanceof StaleSuggestionError) {
        const fresh = error.suggestion;
        setList((current) =>
          fresh ? current.map((s) => (s.key === suggestion.key ? fresh : s)) : current.filter((s) => s.key !== suggestion.key),
        );
        setNote({ key: fresh?.key ?? suggestion.key, kind: "stale" });
      } else {
        setNote({ key: suggestion.key, kind: "error", text: error instanceof Error ? error.message : String(error) });
      }
    } finally {
      setBusy(false);
    }
  };

  const dismiss = async (suggestion: Suggestion) => {
    setList((current) => current.filter((s) => s.key !== suggestion.key));
    try {
      await analyticsApi.dismissSuggestion(suggestion.key, project);
      onChanged?.();
    } catch (error) {
      setList((current) => [...current, suggestion]);
      setNote({ key: suggestion.key, kind: "error", text: error instanceof Error ? error.message : String(error) });
    }
  };

  return (
    <section className="an-section an-suggestions" aria-label={t("analytics.suggestions")}>
      <h4>{t("analytics.suggestions")}</h4>
      {note && (
        <p className={`an-note${note.kind === "applied" ? " is-ok" : " is-warn"}`} role="status">
          {note.kind === "applied" ? t("analytics.applied") : note.kind === "stale" ? t("analytics.stale") : note.text}
        </p>
      )}
      <ul className="an-suggestion-list">
        {list.map((suggestion) => {
          const change = suggestion.change;
          return (
            <li key={suggestion.key} className="an-suggestion">
              <p>{suggestionText(suggestion, t)}</p>
              <div className="an-actions">
                {change?.type === "open_settings" ? (
                  <button type="button" onClick={() => onOpenSettings?.(change.row ?? "")}>
                    {t("analytics.open")}
                  </button>
                ) : change ? (
                  <button type="button" className="primary" onClick={() => setConfirming(suggestion)}>
                    {t("analytics.apply")}
                  </button>
                ) : null}
                <button type="button" className="an-quiet" onClick={() => void dismiss(suggestion)}>
                  {t("analytics.dismiss")}
                </button>
              </div>
            </li>
          );
        })}
      </ul>
      {confirming && (
        <ConfirmApply
          suggestion={confirming}
          busy={busy}
          onCancel={() => setConfirming(null)}
          onConfirm={() => void apply(confirming)}
        />
      )}
    </section>
  );
}
