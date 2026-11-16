/**
 * The runners and today's spend in one pill: a dot (green while any runner is
 * online), "N runners · $today", and a press opens Station. Stands in for the
 * Runners and Usage icons; both pages stay in Settings and the palette.
 *
 * Today's spend is the daily budget's "used" figure (GET /api/runs/budget),
 * the one number the hub already keeps for today. Drawn only while the agent
 * workstation is shown: a board used as a planner has nothing to say here.
 *
 * Single-user edition: there is one machine, so the pill says "This
 * computer" (green while its built-in runner is up), never "runners". While
 * it is not up, the label says which: still starting, or not running.
 */

import { useEffect } from "react";

import { useConnectivity } from "../../api/connectivity";
import { setProjectMode } from "../../station/projectMode";
import { useI18n } from "../../i18n";
import { builtInRunner, isHubRunner, localState, useEdition } from "../../runners/edition";
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
  const listed = useRunners((s) => s.loaded && !(s.loading && s.runners.length === 0));
  const cost = useBudget((s) => s.status?.used.costUsd);
  // Offline, the last list is stale: no runner is known to be up or down.
  const offline = useConnectivity((s) => s.offline);
  const single = useEdition() === "single";

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

  if (offline) {
    const unknown = t(single ? "local.pillUnknown" : "topbar.runnerPill.unknown");
    return (
      <button
        type="button"
        className="topbar-runner-pill"
        onClick={() => setProjectMode("station")}
        title={`${unknown} — ${t("topbar.runnerPill.open")}`}
        aria-label={unknown}
      >
        <span className="runner-dot unknown" aria-hidden />
        <span className="topbar-runner-pill-text" aria-hidden>
          ?
        </span>
      </button>
    );
  }

  const spend = typeof cost === "number" ? formatSpend(cost) : "";
  const local = single ? localState(runners) : undefined;
  const online = single ? (local === "ready" ? 1 : 0) : runners.filter((runner) => runner.online).length;
  const count = single
    ? t("local.title")
    : online === 1
      ? t("topbar.runnerPill.one")
      : t("topbar.runnerPill.many", { count: online });
  // What the grey dot means, once the list has arrived: until then nothing
  // is known, and the pill says only what it always does. With no runner
  // welcomed as built in (serve --no-runner), the dispatch dialog offers an
  // online runner someone connected by hand, so agents can start after all.
  // Once the app's own runner was welcomed the dialog sticks to it, and a
  // runner by hand does not change what the pill says.
  const byHand =
    !builtInRunner(runners) && runners.some((runner) => runner.online && !isHubRunner(runner));
  const label =
    listed && local === "starting"
      ? t("local.pillStarting")
      : listed && local === "stopped" && !byHand
        ? t("local.pillStopped")
        : single
          ? spend
            ? t("local.pill", { cost: spend })
            : t("local.title")
          : spend
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
