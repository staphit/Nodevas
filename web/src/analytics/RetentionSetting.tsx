import { useEffect, useId, useState } from "react";
import { analyticsApi, RETENTION_MAX_DAYS, RETENTION_MIN_DAYS } from "../api/analytics";
import { useI18n } from "../i18n";

export interface RetentionSettingProps {
  isAdmin: boolean;
}

/** What "Forever" goes back to when it is turned off. */
const DEFAULT_DAYS = 180;

const valid = (days: number) => Number.isInteger(days) && days >= RETENTION_MIN_DAYS && days <= RETENTION_MAX_DAYS;

/** How long raw events are kept: the administrator's. */
export default function RetentionSetting({ isAdmin }: RetentionSettingProps) {
  const { t } = useI18n();
  const hintId = useId();
  const errorId = useId();
  const [days, setDays] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  const [invalid, setInvalid] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isAdmin) return;
    let live = true;
    analyticsApi
      .retention()
      .then((value) => {
        if (!live) return;
        setDays(value);
        setDraft(String(value || DEFAULT_DAYS));
      })
      .catch((e: unknown) => live && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      live = false;
    };
  }, [isAdmin]);

  if (!isAdmin) return null;
  if (days === null) return error ? <p className="an-note is-warn">{error}</p> : null;

  const save = async (value: number) => {
    const before = days;
    setDays(value);
    try {
      const saved = await analyticsApi.putRetention(value);
      setDays(saved);
      setError(null);
    } catch (e) {
      setDays(before);
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const commit = () => {
    const value = Number(draft);
    if (!valid(value)) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    if (value !== days) void save(value);
  };

  const forever = days === 0;
  return (
    <div className="an-retention">
      <label className="an-field">
        <span>{t("analytics.retention")}</span>
        <input
          type="number"
          min={RETENTION_MIN_DAYS}
          max={RETENTION_MAX_DAYS}
          aria-label={t("analytics.retention")}
          aria-invalid={invalid}
          aria-describedby={invalid ? errorId : hintId}
          value={forever ? "" : draft}
          disabled={forever}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key === "Enter") commit();
          }}
        />
        <span>{t("analytics.retentionDays", { days: "" }).trim()}</span>
      </label>
      <label className="an-check">
        <input
          type="checkbox"
          checked={forever}
          onChange={() => {
            setInvalid(false);
            if (forever) {
              const back = valid(Number(draft)) ? Number(draft) : DEFAULT_DAYS;
              setDraft(String(back));
              void save(back);
            } else {
              void save(0);
            }
          }}
        />
        <span>{t("analytics.retentionForever")}</span>
      </label>
      {invalid && (
        <p id={errorId} className="an-note is-warn">
          {t("analytics.retentionInvalid")}
        </p>
      )}
      <p id={hintId} className="an-hint">
        {t("analytics.retentionHint")}
      </p>
      {error && <p className="an-note is-warn">{error}</p>}
    </div>
  );
}
