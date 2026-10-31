import { useEffect, useId, useState } from "react";
import { EXPLORATION_MAX_PCT, type AnalyticsSettings, type RoutePick, type Tier, type TiersResponse } from "../api/analytics";
import { useI18n } from "../i18n";
import { roleNameById } from "../roles/roleLabels";
import { useApp } from "../store";
import { agentModelText, pickReasonText } from "./analyticsFormat";
import { TierChip } from "./ScorecardsTable";

export interface TiersRoutingProps {
  tiers: TiersResponse;
  settings: AnalyticsSettings;
  picks: RoutePick[];
  readOnly: boolean;
  /** Saves the settings (PUT /api/analytics/settings). */
  onChange: (settings: AnalyticsSettings) => void;
}

const TIERS: readonly Tier[] = ["probation", "mid", "senior"];
const ROLE_ORDER = ["planner", "qa", "tester", "executor", "reviewer", "researcher"];

function clampPct(raw: string, fallback: number): number {
  const value = Number.parseInt(raw, 10);
  if (!Number.isFinite(value)) return fallback;
  return Math.min(EXPLORATION_MAX_PCT, Math.max(0, value));
}

/** The auto routing switch, required tiers, exploration and recent picks. */
export default function TiersRouting({ tiers, settings, picks, readOnly, onChange }: TiersRoutingProps) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const hintId = useId();
  const shareHintId = useId();
  const [share, setShare] = useState(String(settings.explorationPct));
  useEffect(() => setShare(String(settings.explorationPct)), [settings.explorationPct]);

  const roles = [
    ...ROLE_ORDER.filter((role) => role in tiers.required),
    ...Object.keys(tiers.required).filter((role) => !ROLE_ORDER.includes(role)),
  ];
  const required = (role: string): Tier => settings.requiredTiers[role] ?? tiers.required[role] ?? "mid";
  const tierName = (tier: string) => (tier ? t(`tier.${tier}`) : "");

  const commitShare = () => {
    const pct = clampPct(share, settings.explorationPct);
    setShare(String(pct));
    onChange({ ...settings, explorationPct: pct });
  };

  return (
    <div className="an-tiers">
      <div className="an-switch-row">
        <button
          type="button"
          role="switch"
          className="an-switch"
          aria-checked={settings.autoRouting}
          aria-describedby={hintId}
          disabled={readOnly}
          onClick={() => onChange({ ...settings, autoRouting: !settings.autoRouting })}
        >
          <span className="an-switch-track" aria-hidden="true">
            <span className="an-switch-thumb" />
          </span>
          <span>{t("analytics.autoRouting")}</span>
        </button>
        <p id={hintId} className="an-hint">
          {t("analytics.autoRoutingHint")}
        </p>
      </div>

      <div className="an-required">
        {roles.map((role) => {
          const name = roleNameById(graph, role, t);
          return (
            <label key={role} className="an-field">
              <span>{name}</span>
              <select
                aria-label={t("analytics.requiredTier", { role: name })}
                value={required(role)}
                disabled={readOnly}
                onChange={(event) =>
                  onChange({
                    ...settings,
                    requiredTiers: { ...settings.requiredTiers, [role]: event.target.value as Tier },
                  })
                }
              >
                {TIERS.map((tier) => (
                  <option key={tier} value={tier}>
                    {tierName(tier)}
                  </option>
                ))}
              </select>
            </label>
          );
        })}
        <label className="an-field">
          <span>{t("analytics.exploration")}</span>
          <span className="an-pct">
            <input
              type="number"
              min={0}
              max={EXPLORATION_MAX_PCT}
              step={5}
              aria-label={t("analytics.exploration")}
              aria-describedby={shareHintId}
              value={share}
              disabled={readOnly}
              onChange={(event) => setShare(event.target.value)}
              onBlur={commitShare}
              onKeyDown={(event) => {
                if (event.key === "Enter") commitShare();
              }}
            />
            %
          </span>
        </label>
      </div>
      <p id={shareHintId} className="an-hint">
        {t("analytics.explorationHint")}
      </p>

      <p className="an-hint">{t("analytics.tiersHint", { days: tiers.days, min: tiers.minSamples })}</p>
      {tiers.rows.length > 0 && (
        <div className="an-table-wrap">
          <table className="an-table" aria-label={t("analytics.tab.tiers")}>
            <thead>
              <tr>
                <th scope="col">{t("analytics.col.role")}</th>
                <th scope="col">{t("analytics.col.agent")}</th>
                <th scope="col" className="num">
                  {t("analytics.col.samples")}
                </th>
                <th scope="col" className="num">
                  {t("analytics.col.score")}
                </th>
                <th scope="col">{t("analytics.col.tier")}</th>
              </tr>
            </thead>
            <tbody>
              {tiers.rows.map((row) => (
                <tr key={`${row.role}/${row.agent}/${row.model}`}>
                  <th scope="row">{roleNameById(graph, row.role, t)}</th>
                  <td className="an-mono">{agentModelText({ agent: row.agent, model: row.model })}</td>
                  <td className="num">{row.samples}</td>
                  <td className="num">{row.score.toFixed(2)}</td>
                  <td>
                    <TierChip tier={row.tier} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <section className="an-section" aria-label={t("analytics.picks")}>
        <h4>{t("analytics.picks")}</h4>
        {picks.length === 0 ? (
          <p className="an-empty">{t("analytics.picksNone")}</p>
        ) : (
          <ul className="an-picks">
            {picks.map((pick) => (
              <li key={pick.id} className="an-row">
                <span className="an-row-name">
                  {pick.role ? roleNameById(graph, pick.role, t) : ""}
                  {pick.agent && <span className="an-mono"> · {agentModelText({ agent: pick.agent, model: pick.model })}</span>}
                </span>
                <span className="an-pick-reason">
                  {pickReasonText(pick.reason?.code ?? "", { rule: pick.ruleId, tier: tierName(pick.requiredTier) }, t)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
