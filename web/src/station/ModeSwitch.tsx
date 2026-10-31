// The top bar's Board | Station | Settings switch: the three views, side by
// side, one click each. Part of the main bundle, so it stays small: Station
// and Settings load only when someone opens them.

import { useMemo } from "react";

import { canAnswerAsk, useAgents } from "../agents/agentStore";
import { useAutopilot } from "../autopilot/autopilotStore";
import { setProjectMode } from "./projectMode";
import { useI18n } from "../i18n";
import { IconCanvas, IconSpark } from "../icons";
import { useBudget } from "../runs/budgetStore";
import { useRuns } from "../runs/runStore";
import { useApp, usePreference } from "../store";
import { useStation } from "./stationStore";
import { waitingOnYou } from "./waiting";

/**
 * Everything waiting on the viewer: Station's badge. The same list Station's
 * "Waiting on you" card shows (waiting.ts), counted from the same stores:
 * the questions and held messages they may answer, autopilot reviews, PRs to
 * merge or fix, offers no runner took yet, usage limits and an exceeded daily
 * budget. Station's recent runs count only once Station has loaded them for
 * the project on screen.
 */
export function useWaitingCount(): number {
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const reviews = useAutopilot((s) => s.waiting);
  const active = useRuns((s) => s.activeRuns);
  const runs = useStation((s) => s.runs);
  const runsProject = useStation((s) => s.project);
  const activeProject = useApp((s) => s.activeProject);
  const exceeded = useBudget((s) => s.status?.exceeded);
  return useMemo(
    () =>
      waitingOnYou({
        runs: runsProject === activeProject ? runs : [],
        active,
        asks,
        held,
        reviews,
        budgetExceeded: exceeded,
        canAnswer: (ask) => canAnswerAsk(ask, viewer),
        now: Date.now(),
      }).length,
    [runs, runsProject, activeProject, active, asks, held, reviews, exceeded, viewer],
  );
}

/** A toothed cog for Settings (the shared IconGear reads as a sun). */
function IconCog({ size = 14 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19.11 9.96 21.42 10.17 21.42 13.83 19.11 14.04 18.47 15.59 19.96 17.37 17.37 19.96 15.59 18.47 14.04 19.11 13.83 21.42 10.17 21.42 9.96 19.11 8.41 18.47 6.63 19.96 4.04 17.37 5.53 15.59 4.89 14.04 2.58 13.83 2.58 10.17 4.89 9.96 5.53 8.41 4.04 6.63 6.63 4.04 8.41 5.53 9.96 4.89 10.17 2.58 13.83 2.58 14.04 4.89 15.59 5.53 17.37 4.04 19.96 6.63 18.47 8.41Z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  );
}

export function ModeSwitch() {
  const { t } = useI18n();
  const mode = usePreference("projectMode");
  const waiting = useWaitingCount();
  return (
    <nav className="mode-switch" aria-label={t("mode.label")}>
      <button
        type="button"
        className={mode === "info" ? "active" : ""}
        aria-pressed={mode === "info"}
        onClick={() => setProjectMode("info")}
        title={t("mode.info.detail")}
        aria-label={t("topbar.board")}
      >
        <IconCanvas size={14} />
        <span>{t("topbar.board")}</span>
      </button>
      <button
        type="button"
        className={mode === "station" ? "active" : ""}
        aria-pressed={mode === "station"}
        onClick={() => setProjectMode("station")}
        title={t("mode.station.detail")}
        // The names hide on a narrow bar; the label keeps them, and the count.
        aria-label={waiting > 0 ? `${t("mode.station")}, ${t("mode.waiting", { count: waiting })}` : t("mode.station")}
      >
        <IconSpark size={14} />
        <span>{t("mode.station")}</span>
        {waiting > 0 && (
          <span className="mode-switch-badge" aria-hidden="true">
            {waiting}
          </span>
        )}
      </button>
      <button
        type="button"
        className={mode === "settings" ? "active" : ""}
        aria-pressed={mode === "settings"}
        onClick={() => setProjectMode("settings")}
        title={t("topbar.settingsTitle")}
        aria-label={t("topbar.settings")}
        // The tour's "replay any time" step points here: Replay tour is in
        // Settings › General.
        data-tour="tour-replay"
      >
        <IconCog />
        <span>{t("topbar.settings")}</span>
      </button>
    </nav>
  );
}
