import { useCallback, useEffect, useState } from "react";
import "./analytics.css";
import { useI18n } from "../i18n";
import { useAnalytics } from "./analyticsStore";
import InsightsPanel from "./InsightsPanel";
import RetentionSetting from "./RetentionSetting";
import ScorecardsTable from "./ScorecardsTable";
import SuggestionList from "./SuggestionList";
import TiersRouting from "./TiersRouting";

export interface AnalyticsSettingsProps {
  project?: string;
  isAdmin?: boolean;
  canWrite?: boolean;
  onOpenSettings?: (row: string) => void;
}

type Tab = "insights" | "scorecards" | "tiers";
const TABS: readonly Tab[] = ["insights", "scorecards", "tiers"];

/** Insights, Scorecards and Tiers & routing, as three segmented tabs. */
export default function AnalyticsSettings({ project, isAdmin = false, canWrite = true, onOpenSettings }: AnalyticsSettingsProps) {
  const { t } = useI18n();
  const [tab, setTab] = useState<Tab>("insights");
  const { insights, scorecards, tiers, settings, picks, track, error, readOnly } = useAnalytics();
  const reload = useCallback(() => void useAnalytics.getState().load(project), [project]);
  useEffect(reload, [reload]);

  return (
    <div className="an-panel">
      <p className="an-hint">{t("analytics.description")}</p>
      <div className="an-seg an-tabs" role="group" aria-label={t("analytics.tabs")}>
        {TABS.map((value) => (
          <button key={value} type="button" aria-pressed={value === tab} onClick={() => setTab(value)}>
            {t(`analytics.tab.${value}`)}
          </button>
        ))}
      </div>
      {error && <p className="an-note is-warn">{t("analytics.loadFailed", { error })}</p>}

      {tab === "insights" && (
        <>
          {insights && insights.suggestions.length > 0 && (
            <SuggestionList
              suggestions={insights.suggestions}
              project={project}
              onOpenSettings={onOpenSettings}
              onChanged={reload}
            />
          )}
          <InsightsPanel insights={insights} />
        </>
      )}
      {tab === "scorecards" && scorecards && (
        <ScorecardsTable data={scorecards} track={track} onTrack={(next) => void useAnalytics.getState().setTrack(next)} />
      )}
      {tab === "tiers" && tiers && settings && (
        <>
          <TiersRouting
            tiers={tiers}
            settings={settings}
            picks={picks}
            readOnly={!canWrite || readOnly}
            onChange={(next) => void useAnalytics.getState().saveSettings(next, project)}
          />
          <RetentionSetting isAdmin={isAdmin} />
        </>
      )}
    </div>
  );
}
