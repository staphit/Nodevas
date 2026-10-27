/**
 * The runners and today's spend in one pill: a dot (green while any runner is
 * online), "N runners · $today", and a press opens Station. Stands in for the
 * Runners and Usage icons; both pages stay in Settings and the palette.
 *
 * Today's spend is the daily budget's "used" figure (GET /api/runs/budget),
 * the one number the hub already keeps for today. Drawn only while the agent
 * workstation is shown: a board used as a planner has nothing to say here.
 */

import { useEffect } from "react";

import { setProjectMode } from "../../game/stageSkin";
import { useI18n } from "../../i18n";
import { useWorkstationVisible } from "../../runners/workstation";
import { useRunners } from "../../runners/runnerStore";
import { useBudget } from "../../runs/budgetStore";

/** Today's spend moves only when runs finish; a minute is close enough. */
export const SPEND_REFRESH_MS = 60_000;

export function formatSpend(costUsd: number): string {
  return `$${costUsd.toFixed(2)}`;
}

export function RunnerPill() {
  const { t } = useI18n();
  const visible = useWorkstationVisible();
  const runners = useRunners((s) => s.runners);
  const cost = useBudget((s) => s.status?.used.costUsd);

  useEffect(() => {
    if (!visible) return;
    // Loading the list once makes runners-changed keep it current.
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
    void useBudget.getState().load();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void useBudget.getState().load();
    }, SPEND_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [visible]);

  if (!visible) return null;

  const online = runners.filter((runner) => runner.online).length;
  const count = online === 1 ? t("topbar.runnerPill.one") : t("topbar.runnerPill.many", { count: online });
  const spend = typeof cost === "number" ? formatSpend(cost) : "";
  const label = spend
    ? t("topbar.runnerPill.label", { runners: count, cost: spend })
    : t("topbar.runnerPill.labelNoCost", { runners: count });

  return (
    <button
      type="button"
      className="topbar-runner-pill"
      onClick={() => setProjectMode("station")}
      title={`${label} — ${t("topbar.runnerPill.open")}`}
      aria-label={label}
    >
      <span className={`runner-dot${online > 0 ? " online" : ""}`} aria-hidden />
      <span className="topbar-runner-pill-text" aria-hidden>
        {spend ? `${count} · ${spend}` : count}
      </span>
    </button>
  );
}
