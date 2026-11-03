import { useState } from "react";
import type { CostBucket, Insights } from "../api/analytics";
import { useI18n } from "../i18n";
import { roleNameById } from "../roles/roleLabels";
import { useApp } from "../store";
import { formatUSD } from "../usage/usageFormat";
import { formatCost, formatRate, formatRounds } from "./analyticsFormat";

export interface InsightsPanelProps {
  insights: Insights | null;
}

type Dimension = "role" | "agent" | "model" | "nodeType";
const DIMENSIONS: readonly Dimension[] = ["role", "agent", "model", "nodeType"];

function Tile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="an-tile" title={hint}>
      <span className="an-tile-label">{label}</span>
      <strong className="an-tile-value">{value}</strong>
    </div>
  );
}

function bucketsOf(insights: Insights, dimension: Dimension): CostBucket[] {
  switch (dimension) {
    case "agent":
      return insights.byAgent;
    case "model":
      return insights.byModel;
    case "nodeType":
      return insights.byNodeType;
    default:
      return insights.byRole;
  }
}

/** Cost per delivered task, first pass, rework, waste and its hotspots. */
export default function InsightsPanel({ insights }: InsightsPanelProps) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const [dimension, setDimension] = useState<Dimension>("role");
  if (!insights) return null;
  const { totals, waste } = insights;
  if (totals.runs === 0) {
    return <p className="an-empty">{t("analytics.empty")}</p>;
  }
  const estimated = totals.estimatedCostUsd > 0;
  const buckets = bucketsOf(insights, dimension);
  const keyName = (bucket: CostBucket) => {
    if (dimension === "role") return roleNameById(graph, bucket.key, t);
    if (dimension === "model" && bucket.agent) return `${bucket.agent}/${bucket.key || "default"}`;
    return bucket.key || "—";
  };
  const wasteRows: [string, number][] = [
    ["analytics.waste.failed", waste.failedCostUsd],
    ["analytics.waste.retries", waste.retriedCostUsd],
    ["analytics.waste.abandoned", waste.abandonedCostUsd],
  ];

  return (
    <div className="an-insights">
      <p className="an-window">{t("analytics.window", { days: insights.days })}</p>
      <div className="an-tiles">
        <Tile label={t("analytics.costPerDelivered")} value={formatCost(totals.costPerDelivered, estimated, t)} />
        <Tile label={t("analytics.firstPass")} value={formatRate(totals.firstPassRate)} />
        <Tile label={t("analytics.rework")} value={formatRounds(totals.reworkRounds)} />
        <Tile label={t("analytics.waste")} value={formatUSD(waste.costUsd)} />
      </div>
      {estimated && <p className="an-note">{t("analytics.estimateNote")}</p>}

      <section className="an-section" aria-label={t(`analytics.by.${dimension}`)}>
        <div className="an-seg" role="group" aria-label={t("analytics.costPerDelivered")}>
          {DIMENSIONS.map((d) => (
            <button key={d} type="button" aria-pressed={d === dimension} onClick={() => setDimension(d)}>
              {t(`analytics.by.${d}`)}
            </button>
          ))}
        </div>
        {buckets.length === 0 ? (
          <p className="an-empty">{t("analytics.empty")}</p>
        ) : (
          <div className="an-table-wrap">
            <table className="an-table">
              <thead>
                <tr>
                  <th scope="col">{t(`analytics.by.${dimension}`)}</th>
                  <th scope="col" className="num">
                    {t("explorer.agent.cost")}
                  </th>
                  <th scope="col" className="num">
                    {t("analytics.col.costEff")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {buckets.map((bucket) => (
                  <tr key={`${bucket.agent ?? ""}/${bucket.key}`}>
                    <th scope="row">{keyName(bucket)}</th>
                    <td className="num">{formatUSD(bucket.costUsd)}</td>
                    <td className="num">{formatCost(bucket.costPerDelivered, false, t)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="an-section" aria-label={t("analytics.waste")}>
        <h4>{t("analytics.waste")}</h4>
        <div className="an-rows">
          {wasteRows.map(([key, value]) => (
            <div key={key} className="an-row">
              <span>{t(key)}</span>
              <span className="num">{formatUSD(value)}</span>
            </div>
          ))}
        </div>
      </section>

      {waste.hotspots.length > 0 && (
        <section className="an-section" aria-label={t("analytics.hotspots")}>
          <h4>{t("analytics.hotspots")}</h4>
          <div className="an-rows">
            {waste.hotspots.map((spot) => (
              <div key={spot.nodeId} className="an-row">
                <span className="an-row-name">{spot.nodeTitle || spot.nodeId}</span>
                <span className="num">{formatUSD(spot.costUsd)}</span>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
