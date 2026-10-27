// The top bar's Board | Station switch. Part of the main bundle, so it stays
// small: Station itself loads only when someone opens it. Settings is the
// third mode but not a half of this switch: it is entered from the account
// menu or the palette, and while it shows neither half is pressed, so either
// is the way back.

import { useMemo } from "react";

import { canManageSession, useAgents } from "../agents/agentStore";
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
        canAnswer: (ask) => canManageSession(ask, viewer),
        now: Date.now(),
      }).length,
    [runs, runsProject, activeProject, active, asks, held, reviews, exceeded, viewer],
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
    </nav>
  );
}
