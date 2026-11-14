// Station's two faces as tabs: the project's agents (the board's runs, the
// war room, what waits on you) and the viewer's global agents, which work
// outside any project. The global tab says how many of those are at work.

import { useI18n } from "../i18n";
import { activeGlobalRuns, useGlobalAgents } from "./globalAgentsStore";
import { useStation, type StationTab } from "./stationStore";

const TABS: readonly StationTab[] = ["project", "global"];

/** The tab's id, for the panel's aria-labelledby. */
export function stationTabId(tab: StationTab): string {
  return `station-tab-${tab}`;
}

/** The one panel both tabs control. */
export const STATION_PANEL_ID = "station-tabpanel";

export function StationTabs() {
  const { t } = useI18n();
  const tab = useStation((s) => s.tab);
  const setTab = useStation((s) => s.setTab);
  const active = useGlobalAgents((s) => activeGlobalRuns(s.runs).length);

  const label = (name: StationTab) =>
    name === "project"
      ? t("station.tab.project")
      : active > 0
        ? t("station.tab.globalCount", { count: active })
        : t("station.tab.global");

  const choose = (name: StationTab) => {
    setTab(name);
    document.getElementById(stationTabId(name))?.focus();
  };

  return (
    <div className="station-tabs" role="tablist" aria-label={t("station.tabs")}>
      {TABS.map((name, index) => (
        <button
          key={name}
          type="button"
          role="tab"
          id={stationTabId(name)}
          aria-selected={tab === name}
          aria-controls={STATION_PANEL_ID}
          tabIndex={tab === name ? 0 : -1}
          className={tab === name ? "station-tab active" : "station-tab"}
          onClick={() => setTab(name)}
          onKeyDown={(event) => {
            if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
              event.preventDefault();
              const step = event.key === "ArrowRight" ? 1 : TABS.length - 1;
              choose(TABS[(index + step) % TABS.length]);
            } else if (event.key === "Home" || event.key === "End") {
              event.preventDefault();
              choose(TABS[event.key === "Home" ? 0 : TABS.length - 1]);
            }
          }}
        >
          {label(name)}
        </button>
      ))}
    </div>
  );
}
