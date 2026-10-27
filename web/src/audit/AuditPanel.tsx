import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  ConflictError,
  type AuditEntry,
  type AuditHealth,
  type AuditLimiterStats,
  type AuditScope,
} from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { InlineNotice } from "../components/InteractionPrimitives";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { IconRefresh } from "../icons";
import {
  AUDIT_CATEGORIES,
  auditCategory,
  auditDetailPairs,
  auditFailed,
  filterAudit,
  type AuditCategory,
} from "./auditFormat";

/** Enough to cover a busy day; the server caps a page at 1000. */
const TRAIL_LIMIT = 500;

/** The limiter's route classes (internal/server/abuse.go) and semaphores. */
const LIMITER_CLASSES = ["public-auth", "read", "write", "expensive", "heavy", "ws-upgrade"];
const LIMITER_SEMAPHORES = ["heavy", "docx", "remote"];

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function formatAt(iso: string): string {
  return Number.isFinite(Date.parse(iso)) ? formatLocalizedDateTime(iso) : iso;
}

/**
 * Settings → Audit: the server's security trail for its administrators. Three
 * panes: whether audit rows are reaching the database (and acknowledging the
 * ones that only reached the log), the recent trail, and the rate limiter.
 *
 * Shown only to administrators on a server with accounts; the server refuses
 * everyone else anyway. Every value from the server is drawn as text: entries
 * carry what strangers typed at the sign-in form.
 */
export function AuditPanel() {
  return (
    <>
      <AuditHealthPane />
      <AuditTrailPane />
      <AuditLimiterPane />
    </>
  );
}

/** Whether audit rows are being written, and the reconciliation step after an
 * outage: rows that fell back to the process log have to be found there and
 * acknowledged, by count, before the server reads as healthy again. */
function AuditHealthPane() {
  const { t } = useI18n();
  const [health, setHealth] = useState<AuditHealth | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setHealth(await api.getAuditHealth());
      setError(null);
    } catch (failure) {
      setError(errorText(failure));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const acknowledge = async () => {
    if (!health) return;
    const ok = await confirmAction({
      title: t("audit.health.ackTitle"),
      description: t("audit.health.ackDescription", { count: health.unreconciledEvents }),
      confirmLabel: t("audit.health.ack"),
    });
    if (!ok) return;
    setBusy(true);
    setNotice(null);
    try {
      // The count is a compare-and-swap: it acknowledges exactly the
      // fallbacks this page showed, never one that arrived since.
      setHealth(await api.acknowledgeAuditHealth(health.fallbackEvents));
      setError(null);
      setNotice(t("audit.health.acknowledged"));
    } catch (failure) {
      let message = errorText(failure);
      if (failure instanceof ConflictError) {
        if (failure.code === "audit_writes_degraded") message = t("audit.health.stillFailing");
        else if (failure.code === "audit_fallback_count_changed") message = t("audit.health.countChanged");
        else if (failure.serverMessage) message = failure.serverMessage;
      }
      // Whatever went wrong, what is on screen is stale now.
      const fresh = await api.getAuditHealth().catch(() => null);
      if (fresh) setHealth(fresh);
      setError(message);
    } finally {
      setBusy(false);
    }
  };

  const summary = !health
    ? ""
    : health.status === "unconfigured"
      ? t("audit.health.unconfigured")
      : health.writeStatus === "degraded"
        ? t("audit.health.writesFailing")
        : health.unreconciledEvents > 0
          ? t("audit.health.unreconciled", { count: health.unreconciledEvents })
          : t("audit.health.healthy");

  return (
    <section className="settings-page-pane audit-pane audit-health" aria-label={t("audit.health.title")}>
      <div className="audit-pane-head">
        <h3>{t("audit.health.title")}</h3>
        <button type="button" className="audit-btn" onClick={() => void load()} aria-label={t("audit.health.refresh")}>
          <IconRefresh size={13} />
        </button>
      </div>
      {error && <InlineNotice kind="error">{error}</InlineNotice>}
      {notice && <InlineNotice kind="success">{notice}</InlineNotice>}
      {!health && !error && <p className="audit-empty">{t("common.loading")}</p>}
      {health && (
        <>
          <p className="audit-health-line">
            <span className="audit-status" data-status={health.status}>
              {t(`audit.health.status.${health.status}`)}
            </span>
            <span>{summary}</span>
          </p>
          {health.status !== "unconfigured" && (
            <dl className="audit-facts">
              <div>
                <dt>{t("audit.health.writes")}</dt>
                <dd>{t(`audit.health.status.${health.writeStatus}`)}</dd>
              </div>
              <div>
                <dt>{t("audit.health.fallbacks")}</dt>
                <dd>{health.fallbackEvents}</dd>
              </div>
              <div>
                <dt>{t("audit.health.acknowledgedCount")}</dt>
                <dd>{health.acknowledgedFallbackEvents}</dd>
              </div>
              <div>
                <dt>{t("audit.health.unreconciledCount")}</dt>
                <dd>{health.unreconciledEvents}</dd>
              </div>
              {health.lastFailureAt && (
                <div>
                  <dt>{t("audit.health.lastFailure")}</dt>
                  <dd title={health.lastFailureAt}>{formatAt(health.lastFailureAt)}</dd>
                </div>
              )}
              {health.lastError && (
                <div className="audit-facts-wide">
                  <dt>{t("audit.health.lastError")}</dt>
                  <dd>
                    <code>{health.lastError}</code>
                  </dd>
                </div>
              )}
            </dl>
          )}
          {health.unreconciledEvents > 0 && (
            <div className="audit-ack">
              <p className="settings-hint">{t("audit.health.reconcileHint")}</p>
              <button
                type="button"
                className="audit-btn primary"
                disabled={busy || health.writeStatus !== "healthy"}
                onClick={() => void acknowledge()}
              >
                {t("audit.health.ack")}
              </button>
              {health.writeStatus !== "healthy" && (
                <p className="settings-hint">{t("audit.health.ackBlocked")}</p>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}

/** The recent trail, sorted into kinds and searchable, all on this side:
 * the server's own filters are a prefix and an account ID, which is not how
 * a person looks for "who signed in from where". */
function AuditTrailPane() {
  const { t } = useI18n();
  const [scope, setScope] = useState<AuditScope>("server");
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [category, setCategory] = useState<AuditCategory | "all">("all");
  const [search, setSearch] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await api.getAudit({ scope, limit: TRAIL_LIMIT });
      setEntries(Array.isArray(result.entries) ? result.entries : []);
      setError(null);
    } catch (failure) {
      setError(errorText(failure));
    } finally {
      setLoading(false);
    }
  }, [scope]);

  useEffect(() => {
    void load();
  }, [load]);

  const shown = useMemo(() => filterAudit(entries ?? [], category, search), [entries, category, search]);
  const total = entries?.length ?? 0;

  return (
    <section className="settings-page-pane audit-pane audit-trail" aria-label={t("audit.trail.title")}>
      <div className="audit-pane-head">
        <h3>{t("audit.trail.title")}</h3>
        <button type="button" className="audit-btn" disabled={loading} onClick={() => void load()}>
          <IconRefresh size={13} />
          {t("audit.trail.refresh")}
        </button>
      </div>
      <p className="settings-hint">{t("audit.trail.intro")}</p>
      <div className="audit-filters">
        <select
          value={scope}
          aria-label={t("audit.trail.scope")}
          onChange={(event) => setScope(event.target.value as AuditScope)}
        >
          <option value="server">{t("audit.trail.scope.server")}</option>
          <option value="project">{t("audit.trail.scope.project")}</option>
        </select>
        <select
          value={category}
          aria-label={t("audit.trail.category")}
          onChange={(event) => setCategory(event.target.value as AuditCategory | "all")}
        >
          <option value="all">{t("audit.category.all")}</option>
          {AUDIT_CATEGORIES.map((id) => (
            <option key={id} value={id}>
              {t(`audit.category.${id}`)}
            </option>
          ))}
        </select>
        <input
          type="search"
          value={search}
          placeholder={t("audit.trail.searchPlaceholder")}
          aria-label={t("audit.trail.search")}
          onChange={(event) => setSearch(event.target.value)}
        />
      </div>
      {error && <InlineNotice kind="error">{error}</InlineNotice>}
      {entries === null && !error && <p className="audit-empty">{t("common.loading")}</p>}
      {entries !== null && total === 0 && <p className="audit-empty">{t("audit.trail.empty")}</p>}
      {total > 0 && shown.length === 0 && <p className="audit-empty">{t("audit.trail.noMatch")}</p>}
      {shown.length > 0 && (
        <>
          <p className="audit-count">{t("audit.trail.count", { shown: shown.length, total })}</p>
          <div className="audit-table-wrap">
            <table className="audit-table">
              <thead>
                <tr>
                  <th scope="col">{t("audit.col.time")}</th>
                  <th scope="col">{t("audit.col.action")}</th>
                  <th scope="col">{t("audit.col.actor")}</th>
                  <th scope="col">{t("audit.col.target")}</th>
                  <th scope="col">{t("audit.col.detail")}</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((entry, index) => (
                  <AuditRow key={`${entry.at}-${index}`} entry={entry} />
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

function AuditRow({ entry }: { entry: AuditEntry }) {
  const { t } = useI18n();
  const failed = auditFailed(entry.op);
  const pairs = auditDetailPairs(entry.detail);
  return (
    <tr data-failed={failed ? "true" : undefined}>
      <td className="audit-time" title={entry.at}>
        {formatAt(entry.at)}
      </td>
      <td>
        <code className="audit-op">{entry.op}</code>
        <span className="audit-kind">{t(`audit.category.${auditCategory(entry.op)}`)}</span>
        {failed && <span className="audit-failed">{t("audit.trail.failed")}</span>}
      </td>
      <td>
        <span className="audit-actor">{entry.actor || t("audit.trail.nobody")}</span>
        {entry.id && entry.id !== entry.actor && <small>{entry.id}</small>}
        {entry.ip && <small>{entry.ip}</small>}
      </td>
      <td className="audit-target">{entry.target || "—"}</td>
      <td className="audit-detail">
        {pairs.length === 0
          ? "—"
          : pairs.map(([key, value]) => (
              <span key={key} className="audit-detail-pair">
                <b>{key}</b> {value}
              </span>
            ))}
      </td>
    </tr>
  );
}

/** The rate limiter's counters since the server started: refusals per kind
 * of route say whether someone is hammering the sign-in form, and the busy
 * slots whether imports and exports are queueing. */
function AuditLimiterPane() {
  const { t } = useI18n();
  const [stats, setStats] = useState<AuditLimiterStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setStats(await api.getAuditLimiter());
      setError(null);
    } catch (failure) {
      setError(errorText(failure));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const byClass = stats?.rejected_by_class ?? {};
  // Known classes in a fixed order, then anything a newer server added.
  const classes = [...LIMITER_CLASSES, ...Object.keys(byClass).filter((key) => !LIMITER_CLASSES.includes(key))];
  const semaphores = stats?.semaphores ?? {};

  return (
    <section className="settings-page-pane audit-pane audit-limiter" aria-label={t("audit.limiter.title")}>
      <div className="audit-pane-head">
        <h3>{t("audit.limiter.title")}</h3>
        <button type="button" className="audit-btn" onClick={() => void load()} aria-label={t("audit.limiter.refresh")}>
          <IconRefresh size={13} />
        </button>
      </div>
      {error && <InlineNotice kind="error">{error}</InlineNotice>}
      {!stats && !error && <p className="audit-empty">{t("common.loading")}</p>}
      {stats && !stats.max_entries && <p className="settings-hint">{t("audit.limiter.off")}</p>}
      {stats && !!stats.max_entries && (
        <>
          <p className="settings-hint">{t("audit.limiter.intro")}</p>
          <dl className="audit-facts">
            <div>
              <dt>{t("audit.limiter.rejected")}</dt>
              <dd>{stats.rejected_total ?? 0}</dd>
            </div>
            {classes.map((key) => (
              <div key={key}>
                <dt>{LIMITER_CLASSES.includes(key) ? t(`audit.limiter.class.${key}`) : key}</dt>
                <dd>{byClass[key] ?? 0}</dd>
              </div>
            ))}
            <div>
              <dt>{t("audit.limiter.buckets")}</dt>
              <dd>
                {stats.active_buckets} / {stats.max_entries}
              </dd>
            </div>
            {LIMITER_SEMAPHORES.filter((key) => semaphores[key]).map((key) => (
              <div key={key}>
                <dt>{t(`audit.limiter.slots.${key}`)}</dt>
                <dd>
                  {semaphores[key].in_use} / {semaphores[key].capacity}
                </dd>
              </div>
            ))}
          </dl>
        </>
      )}
    </section>
  );
}
