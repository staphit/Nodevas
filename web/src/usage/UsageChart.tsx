import { useEffect, useId, useRef, useState } from "react";
import type { UsageGroup } from "../api";
import { formatList, useI18n } from "../i18n";
import { formatDayKey, formatTokens, formatTokensExact, formatUSD } from "./usageFormat";

export type UsageMetric = "tokens" | "cost";

/** The stacked token series, bottom up. Colors are the validated first three
 * categorical slots (see 21-usage.css); the order never changes. */
const SERIES = [
  { id: "input", label: "usage.series.input", value: (d: UsageGroup) => d.inputTokens },
  { id: "output", label: "usage.series.output", value: (d: UsageGroup) => d.outputTokens },
  {
    id: "cache",
    label: "usage.series.cache",
    value: (d: UsageGroup) => d.cacheReadTokens + d.cacheWriteTokens,
  },
] as const;

const HEIGHT = 180;
const MARGIN = { top: 10, right: 8, bottom: 22, left: 46 };
/** Surface gap between stacked segments and between adjacent bars. */
const GAP = 2;
const RADIUS = 4;
const MAX_BAR = 28;

/** A round axis maximum at or above `value`, and its step for three gridlines. */
export function niceScale(value: number): { max: number; step: number } {
  if (!(value > 0)) return { max: 1, step: 1 / 3 };
  const rough = value / 3;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= rough) ?? rough;
  return { max: step * Math.ceil(value / step), step };
}

/** A bar segment whose top corners are rounded: the data end, anchored to
 * the baseline below it. */
function roundedTop(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  return [
    `M${x},${y + h}`,
    `V${y + rr}`,
    `Q${x},${y} ${x + rr},${y}`,
    `H${x + w - rr}`,
    `Q${x + w},${y} ${x + w},${y + rr}`,
    `V${y + h}`,
    "Z",
  ].join("");
}

function useWidth(fallback = 560) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(fallback);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.round(entries[0]?.contentRect.width ?? 0);
      if (next > 0) setWidth(next);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
}

/** Daily tokens (stacked input / output / cache) or estimated cost. */
export function UsageChart({ days, metric }: { days: UsageGroup[]; metric: UsageMetric }) {
  const { t } = useI18n();
  const [wrapRef, width] = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const [asTable, setAsTable] = useState(false);
  const titleId = useId();

  const total = (d: UsageGroup) =>
    metric === "cost" ? d.costUsd : SERIES.reduce((sum, s) => sum + s.value(d), 0);
  const formatValue = metric === "cost" ? formatUSD : formatTokens;
  const peak = days.reduce((m, d) => Math.max(m, total(d)), 0);
  const { max, step } = niceScale(peak);

  const plotW = Math.max(40, width - MARGIN.left - MARGIN.right);
  const plotH = HEIGHT - MARGIN.top - MARGIN.bottom;
  const slot = days.length > 0 ? plotW / days.length : plotW;
  const barW = Math.max(1, Math.min(MAX_BAR, slot - GAP));
  const yOf = (v: number) => MARGIN.top + plotH - (v / max) * plotH;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);

  const labelEvery = Math.max(1, Math.ceil(days.length / Math.max(2, Math.floor(plotW / 64))));
  const hovered = hover != null ? days[hover] : undefined;

  const describe = (d: UsageGroup) =>
    metric === "cost"
      ? `${formatDayKey(d.key)}: ${formatUSD(d.costUsd)}`
      : `${formatDayKey(d.key)}: ${formatList(SERIES.map((s) => `${t(s.label)} ${formatTokensExact(s.value(d))}`))}`;

  return (
    <figure className="usage-chart" aria-labelledby={titleId}>
      <figcaption className="usage-chart-head">
        <span id={titleId} className="usage-chart-title">
          {metric === "cost" ? t("usage.chart.costTitle") : t("usage.chart.tokensTitle")}
        </span>
        {metric === "tokens" && (
          <ul className="usage-legend" aria-label={t("usage.chart.legend")}>
            {SERIES.map((s) => (
              <li key={s.id}>
                <span className={`usage-swatch usage-swatch-${s.id}`} aria-hidden="true" />
                {t(s.label)}
              </li>
            ))}
          </ul>
        )}
        <button
          type="button"
          className="usage-link-btn"
          aria-pressed={asTable}
          onClick={() => setAsTable((v) => !v)}
        >
          {asTable ? t("usage.chart.showChart") : t("usage.chart.showTable")}
        </button>
      </figcaption>

      {asTable ? (
        <div className="usage-table-wrap">
          <table className="usage-table">
            <thead>
              <tr>
                <th scope="col">{t("usage.col.day")}</th>
                {metric === "tokens" ? (
                  SERIES.map((s) => (
                    <th key={s.id} scope="col" className="num">
                      {t(s.label)}
                    </th>
                  ))
                ) : (
                  <th scope="col" className="num">
                    {t("usage.col.cost")}
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {days.map((d) => (
                <tr key={d.key}>
                  <th scope="row">{formatDayKey(d.key)}</th>
                  {metric === "tokens" ? (
                    SERIES.map((s) => (
                      <td key={s.id} className="num">
                        {formatTokensExact(s.value(d))}
                      </td>
                    ))
                  ) : (
                    <td className="num">{formatUSD(d.costUsd)}</td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="usage-chart-plot" ref={wrapRef} onMouseLeave={() => setHover(null)}>
          <svg width={width} height={HEIGHT} role="img" aria-label={t("usage.chart.aria")}>
            {ticks.map((v) => (
              <g key={v} className="usage-grid">
                <line x1={MARGIN.left} x2={MARGIN.left + plotW} y1={yOf(v)} y2={yOf(v)} />
                <text x={MARGIN.left - 6} y={yOf(v)} dy="0.32em" textAnchor="end">
                  {formatValue(v)}
                </text>
              </g>
            ))}
            {days.map((d, i) => {
              const x = MARGIN.left + i * slot + (slot - barW) / 2;
              const values = metric === "cost" ? [d.costUsd] : SERIES.map((s) => s.value(d));
              const ids = metric === "cost" ? ["cost"] : SERIES.map((s) => s.id);
              const topIndex = values.reduce((last, v, k) => (v > 0 ? k : last), -1);
              let base = 0;
              const segments = values.map((v, k) => {
                if (!(v > 0)) return null;
                const y0 = yOf(base);
                base += v;
                const y1 = yOf(base);
                // The gap comes off the bottom of every segment above another.
                const bottom = k > 0 && base - v > 0 ? y0 - GAP : y0;
                const h = Math.max(0, bottom - y1);
                if (h <= 0) return null;
                return k === topIndex ? (
                  <path
                    key={ids[k]}
                    className={`usage-bar usage-bar-${ids[k]}`}
                    d={roundedTop(x, y1, barW, h, RADIUS)}
                  />
                ) : (
                  <rect
                    key={ids[k]}
                    className={`usage-bar usage-bar-${ids[k]}`}
                    x={x}
                    y={y1}
                    width={barW}
                    height={h}
                  />
                );
              });
              return (
                <g key={d.key} className={hover === i ? "usage-col hovered" : "usage-col"}>
                  {segments}
                  <rect
                    className="usage-hit"
                    x={MARGIN.left + i * slot}
                    y={MARGIN.top}
                    width={slot}
                    height={plotH}
                    tabIndex={0}
                    aria-label={describe(d)}
                    onMouseEnter={() => setHover(i)}
                    onFocus={() => setHover(i)}
                    onBlur={() => setHover(null)}
                  />
                  {/* Every nth day, and the last one, unless an nth label
                      would sit right against it. */}
                  {(i === days.length - 1 ||
                    (i % labelEvery === 0 && days.length - 1 - i >= labelEvery)) && (
                    <text
                      className="usage-axis-label"
                      x={MARGIN.left + i * slot + slot / 2}
                      y={HEIGHT - 6}
                      textAnchor="middle"
                    >
                      {formatDayKey(d.key)}
                    </text>
                  )}
                </g>
              );
            })}
            <line
              className="usage-baseline"
              x1={MARGIN.left}
              x2={MARGIN.left + plotW}
              y1={yOf(0)}
              y2={yOf(0)}
            />
          </svg>
          {hovered && hover != null && (
            <div
              className="usage-tooltip"
              role="status"
              style={{
                left: Math.min(
                  Math.max(MARGIN.left + hover * slot + slot / 2, 80),
                  Math.max(80, width - 80),
                ),
              }}
            >
              <strong>{formatDayKey(hovered.key)}</strong>
              {metric === "cost" ? (
                <span>{formatUSD(hovered.costUsd)}</span>
              ) : (
                SERIES.map((s) => (
                  <span key={s.id} className="usage-tooltip-row">
                    <span className={`usage-swatch usage-swatch-${s.id}`} aria-hidden="true" />
                    {t(s.label)} <b>{formatTokensExact(s.value(hovered))}</b>
                  </span>
                ))
              )}
            </div>
          )}
        </div>
      )}
    </figure>
  );
}
