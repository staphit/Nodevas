import type { ScorecardRow, Scorecards, Track } from "../api/analytics";
import { useI18n } from "../i18n";
import { roleNameById } from "../roles/roleLabels";
import { useApp } from "../store";
import { formatCost, formatRate, formatRounds, formatSeconds } from "./analyticsFormat";

export interface ScorecardsTableProps {
  data: Scorecards;
  track: Track;
  onTrack: (track: Track) => void;
}

const TRACKS: readonly Track[] = ["all", "code", "story"];

/** A tier as a small chip; its class carries the colour. */
export function TierChip({ tier }: { tier: string }) {
  const { t } = useI18n();
  return <span className={`an-chip tier--${tier}`}>{t(`tier.${tier}`)}</span>;
}

/** One row per role x agent x model. */
export default function ScorecardsTable({ data, track, onTrack }: ScorecardsTableProps) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const columns: [string, (row: ScorecardRow) => string, string?][] = [
    ["analytics.col.samples", (row) => String(row.samples)],
    [
      "analytics.col.delivery",
      (row) => formatRate(row.delivery),
      t("analytics.adjusted"),
    ],
    ["analytics.col.firstPass", (row) => formatRate(row.firstPassRate)],
    ["analytics.col.rework", (row) => formatRounds(row.reworkRounds)],
    ["analytics.col.costEff", (row) => formatCost(row.costPerDelivered, row.costEstimated, t)],
    ["analytics.col.speed", (row) => formatSeconds(row.medianSeconds)],
    ["analytics.col.reliability", (row) => formatRate(row.reliability)],
    ["analytics.col.tdd", (row) => formatRate(row.tddRate)],
    ["analytics.col.accuracy", (row) => formatRate(row.accuracy)],
    ["analytics.col.score", (row) => row.score.toFixed(2)],
  ];

  return (
    <div className="an-scorecards">
      <div className="an-seg" role="group" aria-label={t("analytics.trackLabel")}>
        {TRACKS.map((value) => (
          <button key={value} type="button" aria-pressed={value === track} onClick={() => onTrack(value)}>
            {t(`analytics.track.${value}`)}
          </button>
        ))}
      </div>
      {data.rows.length === 0 ? (
        <p className="an-empty">{t("analytics.empty")}</p>
      ) : (
        <div className="an-table-wrap">
          <table className="an-table" aria-label={t("analytics.tab.scorecards")}>
            <thead>
              <tr>
                <th scope="col">{t("analytics.col.role")}</th>
                <th scope="col">{t("analytics.col.agent")}</th>
                <th scope="col">{t("analytics.col.model")}</th>
                {columns.map(([key]) => (
                  <th key={key} scope="col" className="num">
                    {t(key)}
                  </th>
                ))}
                <th scope="col">{t("analytics.col.tier")}</th>
              </tr>
            </thead>
            <tbody>
              {data.rows.map((row) => (
                <tr key={`${row.role}/${row.track}/${row.agent}/${row.model}`}>
                  <th scope="row">{roleNameById(graph, row.role, t)}</th>
                  <td>{row.agent}</td>
                  <td className="an-mono">{row.model || "default"}</td>
                  {columns.map(([key, cell, title]) => (
                    <td
                      key={key}
                      className="num"
                      title={
                        key === "analytics.col.delivery" && row.deliveryAdjusted !== null
                          ? `${title} ${formatRate(row.deliveryAdjusted)}`
                          : undefined
                      }
                    >
                      {cell(row)}
                    </td>
                  ))}
                  <td>
                    <TierChip tier={row.tier} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
