import { useEffect, useMemo, useState } from "react";
import { DialogFrame, useInlineDialog } from "../components/DialogFrame";
import {
  USAGE_RANGES,
  type ModelPrice,
  type UsageGroup,
  type UsageModelGroup,
  type UsageReport,
  type UsageSession,
} from "../api";
import { useI18n } from "../i18n";
import { IconClose, IconRefresh, IconUsage, IconWarning } from "../icons";
import { relativeTime } from "../runners/RunnersModal";
import { openNodeRuns } from "../runs/openRuns";
import { UsageChart, type UsageMetric } from "./UsageChart";
import { formatShare, formatTokens, formatTokensExact, formatUSD, perDay } from "./usageFormat";
import { useUsage, type EditablePrice } from "./usageStore";

type Tab = "overview" | "sessions" | "prices";
const TABS: Tab[] = ["overview", "sessions", "prices"];

/** A day estimate scaled to a 30-day month, the unit plans are sold in. */
const MONTH_DAYS = 30;

function Tile({ label, value, hint, title }: { label: string; value: string; hint?: string; title?: string }) {
  return (
    <div className="usage-tile" title={title}>
      <span className="usage-tile-label">{label}</span>
      <strong className="usage-tile-value">{value}</strong>
      {hint && <span className="usage-tile-hint">{hint}</span>}
    </div>
  );
}

function Summary({ report }: { report: UsageReport }) {
  const { t } = useI18n();
  const { totals, days } = report;
  const monthly = perDay(totals.costUsd, days) * MONTH_DAYS;
  return (
    <div className="usage-tiles">
      <Tile
        label={t("usage.tile.tokens")}
        value={formatTokens(totals.totalTokens)}
        title={formatTokensExact(totals.totalTokens)}
        hint={t("usage.tile.tokensHint", {
          input: formatTokens(totals.inputTokens),
          output: formatTokens(totals.outputTokens),
        })}
      />
      <Tile label={t("usage.tile.cost")} value={formatUSD(totals.costUsd)} hint={t("usage.tile.costHint")} />
      <Tile
        label={t("usage.tile.monthly")}
        value={formatUSD(monthly)}
        hint={t("usage.tile.monthlyHint", { perDay: formatUSD(perDay(totals.costUsd, days)) })}
      />
      <Tile
        label={t("usage.tile.activity")}
        value={`${totals.runs} · ${totals.sessions}`}
        hint={t("usage.tile.activityValue", { runs: totals.runs, sessions: totals.sessions })}
      />
    </div>
  );
}

function UnpricedWarning({ report, onAddPrices }: { report: UsageReport; onAddPrices: (models: string[]) => void }) {
  const { t } = useI18n();
  if (!(report.totals.unpricedTokens > 0)) return null;
  const models = report.byModel.filter((m) => m.unpricedTokens > 0).map((m) => m.key);
  return (
    <div className="usage-warning" role="status">
      <IconWarning size={14} />
      <span>
        {t("usage.unpriced", {
          tokens: formatTokens(report.totals.unpricedTokens),
          models: models.join(", ") || t("usage.unknownModel"),
        })}
      </span>
      <button type="button" className="usage-link-btn" onClick={() => onAddPrices(models)}>
        {t("usage.addPrices")}
      </button>
    </div>
  );
}

function TokenSplit({ group }: { group: UsageGroup }) {
  const { t } = useI18n();
  return (
    <span
      className="usage-split"
      title={t("usage.splitTitle", {
        input: formatTokensExact(group.inputTokens),
        output: formatTokensExact(group.outputTokens),
        cache: formatTokensExact(group.cacheReadTokens + group.cacheWriteTokens),
      })}
    >
      {formatTokens(group.inputTokens)} / {formatTokens(group.outputTokens)} /{" "}
      {formatTokens(group.cacheReadTokens + group.cacheWriteTokens)}
    </span>
  );
}

function CostCell({ group }: { group: UsageGroup }) {
  const { t } = useI18n();
  return (
    <td className="num">
      {formatUSD(group.costUsd)}
      {group.unpricedTokens > 0 && (
        <span className="usage-flag" title={t("usage.partlyUnpriced")} aria-label={t("usage.partlyUnpriced")}>
          *
        </span>
      )}
    </td>
  );
}

function AgentTable({ report }: { report: UsageReport }) {
  const { t } = useI18n();
  if (report.byAgent.length === 0) return null;
  return (
    <section className="usage-section" aria-label={t("usage.byAgent")}>
      <h3>{t("usage.byAgent")}</h3>
      <p className="usage-note">{t("usage.byAgentHint")}</p>
      <div className="usage-table-wrap">
        <table className="usage-table">
          <thead>
            <tr>
              <th scope="col">{t("usage.col.agent")}</th>
              <th scope="col" className="num">
                {t("usage.col.tokens")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.cost")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.share")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.tokensPerDay")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.costPerDay")}
              </th>
            </tr>
          </thead>
          <tbody>
            {report.byAgent.map((g) => (
              <tr key={g.key}>
                <th scope="row">{g.key || t("usage.unknownAgent")}</th>
                <td className="num" title={formatTokensExact(g.totalTokens)}>
                  {formatTokens(g.totalTokens)}
                </td>
                <CostCell group={g} />
                <td className="num">
                  {report.totals.costUsd > 0
                    ? formatShare(g.costUsd, report.totals.costUsd)
                    : formatShare(g.totalTokens, report.totals.totalTokens)}
                </td>
                <td className="num">{formatTokens(perDay(g.totalTokens, report.days))}</td>
                <td className="num">{formatUSD(perDay(g.costUsd, report.days))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function ModelTable({ models, totals }: { models: UsageModelGroup[]; totals: UsageReport["totals"] }) {
  const { t } = useI18n();
  if (models.length === 0) return null;
  return (
    <section className="usage-section" aria-label={t("usage.byModel")}>
      <h3>{t("usage.byModel")}</h3>
      <div className="usage-table-wrap">
        <table className="usage-table">
          <thead>
            <tr>
              <th scope="col">{t("usage.col.model")}</th>
              <th scope="col">{t("usage.col.agent")}</th>
              <th scope="col" className="num" title={t("usage.splitHeader")}>
                {t("usage.col.split")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.cost")}
              </th>
              <th scope="col" className="num">
                {t("usage.col.share")}
              </th>
            </tr>
          </thead>
          <tbody>
            {models.map((m) => (
              <tr key={`${m.agent}/${m.key}`}>
                <th scope="row" className="usage-model">
                  {m.key || t("usage.unknownModel")}
                </th>
                <td>{m.agent}</td>
                <td className="num">
                  <TokenSplit group={m} />
                </td>
                <CostCell group={m} />
                <td className="num">{formatShare(m.totalTokens, totals.totalTokens)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

type SortKey = "last" | "cost" | "tokens";

function SessionTable({ sessions, onOpenRun }: { sessions: UsageSession[]; onOpenRun: (s: UsageSession) => void }) {
  const { t } = useI18n();
  const [sort, setSort] = useState<SortKey>("last");
  const sorted = useMemo(() => {
    const list = [...sessions];
    if (sort === "cost") list.sort((a, b) => b.costUsd - a.costUsd || b.totalTokens - a.totalTokens);
    else if (sort === "tokens") list.sort((a, b) => b.totalTokens - a.totalTokens);
    else list.sort((a, b) => Date.parse(b.lastAt || "0") - Date.parse(a.lastAt || "0"));
    return list;
  }, [sessions, sort]);

  if (sessions.length === 0) {
    return <p className="usage-empty">{t("usage.noSessions")}</p>;
  }
  const header = (key: SortKey, label: string) => (
    <th scope="col" className="num" aria-sort={sort === key ? "descending" : "none"}>
      <button type="button" className={`usage-sort${sort === key ? " active" : ""}`} onClick={() => setSort(key)}>
        {label}
        {sort === key && <span aria-hidden="true"> ↓</span>}
      </button>
    </th>
  );
  return (
    <div className="usage-table-wrap">
      <table className="usage-table usage-sessions">
        <thead>
          <tr>
            <th scope="col">{t("usage.col.session")}</th>
            {header("tokens", t("usage.col.tokens"))}
            {header("cost", t("usage.col.cost"))}
            {header("last", t("usage.col.last"))}
            <th scope="col">{t("usage.col.agent")}</th>
            <th scope="col">{t("usage.col.model")}</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((s) => {
            const models = s.models.length > 0 ? s.models : s.model ? [s.model] : [];
            const name = s.name || s.id;
            return (
              <tr key={`${s.source}:${s.id}`} data-testid={`usage-session-${s.id}`}>
                <th scope="row">
                  <span className={`usage-source usage-source-${s.source}`}>
                    {s.source === "run" ? t("usage.source.run") : t("usage.source.session")}
                  </span>
                  {s.source === "run" && s.nodeId ? (
                    <button type="button" className="usage-link-btn" onClick={() => onOpenRun(s)}>
                      {name}
                    </button>
                  ) : (
                    <span className="usage-session-name">{name}</span>
                  )}
                  {s.actorName && <span className="usage-actor">{s.actorName}</span>}
                </th>
                <td className="num" title={formatTokensExact(s.totalTokens)}>
                  {formatTokens(s.totalTokens)}
                </td>
                <CostCell group={{ ...s, key: s.id }} />
                <td className="num">{s.lastAt ? relativeTime(s.lastAt) : "—"}</td>
                <td>{s.agent}</td>
                <td className="usage-model usage-models">{models.join(", ") || "—"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

type PriceDraft = { [K in keyof EditablePrice]: string } & { key: number };

const PRICE_FIELDS = [
  ["inputPerMTok", "usage.price.input"],
  ["outputPerMTok", "usage.price.output"],
  ["cacheReadPerMTok", "usage.price.cacheRead"],
  ["cacheWritePerMTok", "usage.price.cacheWrite"],
] as const;

let draftKey = 0;
function toDraft(p: Partial<ModelPrice> & { model: string }): PriceDraft {
  return {
    key: ++draftKey,
    model: p.model,
    inputPerMTok: p.inputPerMTok != null ? String(p.inputPerMTok) : "",
    outputPerMTok: p.outputPerMTok != null ? String(p.outputPerMTok) : "",
    cacheReadPerMTok: p.cacheReadPerMTok != null ? String(p.cacheReadPerMTok) : "",
    cacheWritePerMTok: p.cacheWritePerMTok != null ? String(p.cacheWritePerMTok) : "",
  };
}

/** A draft as a price, or the reason it is not one. */
function parseDraft(d: PriceDraft): EditablePrice | "model" | "number" {
  const model = d.model.trim();
  if (!model) return "model";
  const out: EditablePrice = {
    model,
    inputPerMTok: 0,
    outputPerMTok: 0,
    cacheReadPerMTok: 0,
    cacheWritePerMTok: 0,
  };
  for (const [field] of PRICE_FIELDS) {
    const raw = d[field].trim();
    const value = raw === "" ? 0 : Number(raw);
    if (!Number.isFinite(value) || value < 0) return "number";
    out[field] = value;
  }
  return out;
}

function PricesPanel({ suggested }: { suggested: string[] }) {
  const { t } = useI18n();
  const prices = useUsage((s) => s.prices);
  const loading = useUsage((s) => s.pricesLoading);
  const error = useUsage((s) => s.pricesError);
  const forbidden = useUsage((s) => s.pricesForbidden);
  const saving = useUsage((s) => s.saving);
  const loadPrices = useUsage((s) => s.loadPrices);
  const savePrices = useUsage((s) => s.savePrices);
  const [drafts, setDrafts] = useState<PriceDraft[] | null>(null);
  const [invalid, setInvalid] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    void loadPrices();
  }, [loadPrices]);

  const custom = useMemo(() => (prices?.prices ?? []).filter((p) => p.source === "custom"), [prices]);
  const builtin = useMemo(() => (prices?.prices ?? []).filter((p) => p.source === "builtin"), [prices]);

  // Drafts start from the saved custom prices, plus a row for each model the
  // warning sent us here for.
  useEffect(() => {
    if (!prices || drafts) return;
    const have = new Set(custom.map((p) => p.model.toLowerCase()));
    const extra = suggested.filter((m) => m && !have.has(m.toLowerCase())).map((model) => toDraft({ model }));
    setDrafts([...custom.map(toDraft), ...extra]);
  }, [prices, custom, suggested, drafts]);

  const update = (key: number, field: keyof EditablePrice, value: string) => {
    setSaved(false);
    setDrafts((list) => (list ?? []).map((d) => (d.key === key ? { ...d, [field]: value } : d)));
  };

  const save = async () => {
    const parsed = (drafts ?? []).map(parseDraft);
    if (parsed.includes("model")) return setInvalid(t("usage.price.needModel"));
    if (parsed.includes("number")) return setInvalid(t("usage.price.needNumber"));
    setInvalid("");
    const ok = await savePrices(parsed as EditablePrice[]);
    if (ok) {
      setSaved(true);
      setDrafts(null);
    }
  };

  const overridden = new Set((drafts ?? []).map((d) => d.model.trim().toLowerCase()));

  return (
    <div className="usage-prices">
      <p className="usage-note">{t("usage.price.note", { asOf: prices?.asOf || "—" })}</p>
      {error && (
        <div className="notify-message error" role="status">
          {error}
        </div>
      )}
      {forbidden && (
        <div className="notify-message error" role="status">
          {t("usage.price.forbidden")}
        </div>
      )}
      {invalid && (
        <div className="notify-message error" role="alert">
          {invalid}
        </div>
      )}
      {saved && (
        <div className="notify-message ok" role="status">
          {t("usage.price.saved")}
        </div>
      )}

      <section className="usage-section" aria-label={t("usage.price.custom")}>
        <h3>{t("usage.price.custom")}</h3>
        {(drafts ?? []).length === 0 && <p className="usage-empty">{t("usage.price.noCustom")}</p>}
        {(drafts ?? []).map((d) => (
          <div key={d.key} className="usage-price-row" data-testid="usage-price-draft">
            <input
              className="usage-price-model"
              value={d.model}
              placeholder={t("usage.price.modelPlaceholder")}
              aria-label={t("usage.price.model")}
              onChange={(e) => update(d.key, "model", e.target.value)}
            />
            {PRICE_FIELDS.map(([field, label]) => (
              <label key={field} className="usage-price-field">
                <span>{t(label)}</span>
                <input
                  inputMode="decimal"
                  value={d[field]}
                  placeholder="0"
                  aria-label={`${t(label)} — ${d.model || t("usage.price.model")}`}
                  onChange={(e) => update(d.key, field, e.target.value)}
                />
              </label>
            ))}
            <button
              type="button"
              className="usage-link-btn"
              onClick={() => {
                setSaved(false);
                setDrafts((list) => (list ?? []).filter((x) => x.key !== d.key));
              }}
            >
              {t("usage.price.remove")}
            </button>
          </div>
        ))}
        <div className="usage-price-actions">
          <button
            type="button"
            className="usage-btn"
            onClick={() => {
              setSaved(false);
              setDrafts((list) => [...(list ?? []), toDraft({ model: "" })]);
            }}
          >
            {t("usage.price.add")}
          </button>
          <button type="button" className="usage-btn primary" disabled={saving || !drafts} onClick={() => void save()}>
            {saving ? t("usage.price.saving") : t("usage.price.save")}
          </button>
        </div>
      </section>

      <section className="usage-section" aria-label={t("usage.price.builtin")}>
        <h3>{t("usage.price.builtin")}</h3>
        {loading && !prices ? (
          <p className="usage-empty">{t("common.loading")}</p>
        ) : (
          <div className="usage-table-wrap">
            <table className="usage-table">
              <thead>
                <tr>
                  <th scope="col">{t("usage.price.model")}</th>
                  {PRICE_FIELDS.map(([field, label]) => (
                    <th key={field} scope="col" className="num">
                      {t(label)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {builtin.map((p) => (
                  <tr key={p.model} className={overridden.has(p.model.toLowerCase()) ? "usage-overridden" : undefined}>
                    <th scope="row" className="usage-model">
                      {p.model}
                      {overridden.has(p.model.toLowerCase()) && (
                        <span className="usage-actor">{t("usage.price.overridden")}</span>
                      )}
                    </th>
                    {PRICE_FIELDS.map(([field]) => (
                      <td key={field} className="num">
                        ${p[field]}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

/**
 * Token use and its API-equivalent cost, for runs and for the agent
 * network's own sessions: what was used, by whom, on which model, and what it
 * would cost per month at this rate, to decide on a plan or a harness.
 */
export function UsageModal({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const inline = useInlineDialog();
  const report = useUsage((s) => s.report);
  const loading = useUsage((s) => s.loading);
  const error = useUsage((s) => s.error);
  const days = useUsage((s) => s.days);
  const refresh = useUsage((s) => s.refresh);
  const setDays = useUsage((s) => s.setDays);
  const [tab, setTab] = useState<Tab>("overview");
  const [metric, setMetric] = useState<UsageMetric>("tokens");
  const [suggested, setSuggested] = useState<string[]>([]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Realtime events refetch only while this is showing.
  useEffect(() => {
    useUsage.getState().setOpen(true);
    return () => useUsage.getState().setOpen(false);
  }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !inline) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, inline]);

  const tabLabel: Record<Tab, string> = {
    overview: t("usage.tab.overview"),
    sessions: t("usage.tab.sessions"),
    prices: t("usage.tab.prices"),
  };

  return (
    <DialogFrame className="usage-dialog usage-root" label={t("usage.title")} onDismiss={onClose}>
      <header>
        <span className="confirm-dialog-icon">
          <IconUsage size={17} />
        </span>
        <div>
          <h2>{t("usage.title")}</h2>
          <p>{t("usage.description")}</p>
        </div>
        <button
          type="button"
          className="confirm-dialog-close"
          data-dialog-close
          onClick={onClose}
          aria-label={t("common.close")}
        >
          <IconClose size={14} />
        </button>
      </header>

      <div className="usage-toolbar">
        <div className="usage-tablist" role="tablist" aria-label={t("usage.title")}>
          {TABS.map((name) => (
            <button
              key={name}
              type="button"
              role="tab"
              id={`usage-tab-${name}`}
              aria-selected={tab === name}
              aria-controls={`usage-panel-${name}`}
              className={tab === name ? "active" : undefined}
              onClick={() => setTab(name)}
            >
              {tabLabel[name]}
            </button>
          ))}
        </div>
        {tab !== "prices" && (
          <div className="usage-range" role="group" aria-label={t("usage.range")}>
            {USAGE_RANGES.map((n) => (
              <button
                key={n}
                type="button"
                aria-pressed={days === n}
                className={days === n ? "active" : undefined}
                onClick={() => void setDays(n)}
              >
                {n === 1 ? t("usage.range.today") : t("usage.range.days", { n })}
              </button>
            ))}
            <button
              type="button"
              className="usage-icon-btn"
              onClick={() => void refresh()}
              aria-label={t("usage.refresh")}
              title={t("usage.refresh")}
              disabled={loading}
            >
              <IconRefresh size={13} />
            </button>
          </div>
        )}
      </div>

      <div className="usage-body" role="tabpanel" id={`usage-panel-${tab}`} aria-labelledby={`usage-tab-${tab}`}>
        {error && tab !== "prices" && (
          <div className="notify-message error" role="status">
            {error}
          </div>
        )}
        {tab === "prices" ? (
          <PricesPanel suggested={suggested} />
        ) : !report ? (
          <p className="usage-empty">{loading ? t("common.loading") : t("usage.empty")}</p>
        ) : tab === "overview" ? (
          <>
            <Summary report={report} />
            <UnpricedWarning
              report={report}
              onAddPrices={(models) => {
                setSuggested(models);
                setTab("prices");
              }}
            />
            {report.totals.totalTokens === 0 ? (
              <p className="usage-empty">{t("usage.empty")}</p>
            ) : (
              <>
                <div className="usage-metric" role="group" aria-label={t("usage.chart.metric")}>
                  {(["tokens", "cost"] as const).map((m) => (
                    <button
                      key={m}
                      type="button"
                      aria-pressed={metric === m}
                      className={metric === m ? "active" : undefined}
                      onClick={() => setMetric(m)}
                    >
                      {m === "tokens" ? t("usage.metric.tokens") : t("usage.metric.cost")}
                    </button>
                  ))}
                </div>
                <UsageChart days={report.byDay} metric={metric} />
                <AgentTable report={report} />
                <ModelTable models={report.byModel} totals={report.totals} />
              </>
            )}
            <p className="usage-note">{t("usage.footnote", { asOf: report.pricesAsOf || "—" })}</p>
          </>
        ) : (
          <SessionTable
            sessions={report.sessions}
            onOpenRun={(s) => {
              onClose();
              openNodeRuns(s.nodeId, s.runId || undefined);
            }}
          />
        )}
      </div>
    </DialogFrame>
  );
}
