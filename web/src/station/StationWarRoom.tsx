// Station's war room section: the building, drawn straight away (an office
// per project, the current one in full, a lounge and a server room), with
// what it does not draw in tiles under it. A character's menu holds what the
// run can be asked to do: open it, its changes, its node or goal, pause or
// resume its autopilot, cancel it. With no runner and nothing under way, a
// call to add a runner comes first.

import { Suspense, useMemo } from "react";

import type { Run } from "../api";
import { useAutopilot } from "../autopilot/autopilotStore";
import { mergeRuns } from "../game/useWorld";
import type { World } from "../game/world";
import { useI18n } from "../i18n";
import { lazyWithRetry } from "../lazyWithRetry";
import { useRuns } from "../runs/runStore";
import { openSettings } from "../settings/openSettings";
import { autopilotIndex, autopilotOf, controlRows, liveAutopilots, type ControlRow } from "./control";
import { useControlLoads, useNoRunner, useTitleOf } from "./StationControl";
import { useStation } from "./stationStore";
import { WarRoomExtras } from "./WarRoomExtras";

const WarRoom = lazyWithRetry(() => import("../game/warroom/WarRoom"));

/** Each run in view, with the autopilot it is a step of: what a
 * character's menu acts on. Stable until a run or an autopilot changes, so
 * the room's memoised characters stay put between heartbeats. */
function useRowOf(): (runId: string) => ControlRow | undefined {
  const active = useRuns((s) => s.activeRuns);
  const latestByNode = useRuns((s) => s.latestByNode);
  const runsByNode = useRuns((s) => s.runsByNode);
  const recent = useStation((s) => s.runs);
  const views = useAutopilot((s) => s.views);
  return useMemo(() => {
    const index = autopilotIndex(views);
    const runs: Run[] = [...recent, ...mergeRuns(active, latestByNode, runsByNode)];
    const rows = new Map<string, ControlRow>();
    for (const run of runs) rows.set(run.id, { run, autopilot: autopilotOf(index, run) });
    return (runId: string) => rows.get(runId);
  }, [active, latestByNode, runsByNode, recent, views]);
}

export function StationWarRoom({ world }: { world: World }) {
  const { t } = useI18n();
  useControlLoads();
  const active = useRuns((s) => s.activeRuns);
  const recent = useStation((s) => s.runs);
  const views = useAutopilot((s) => s.views);
  const titleOf = useTitleOf();
  const rowOf = useRowOf();
  // The world's clock moves on the presence tick: "the last hour" needs no
  // finer one, and the room does not redraw every second.
  const now = world.now;
  const { live, ended } = useMemo(() => controlRows(active, recent, views, now), [active, recent, views, now]);
  const pilots = useMemo(() => liveAutopilots(views), [views]);
  const noRunner = useNoRunner(live.length + pilots.length + world.building.actors.length > 0);
  const count = t("station.control.activeCount", { count: live.length });

  return (
    <section className="station-panel station-warroom" aria-labelledby="station-warroom-title">
      <div className="station-panel-head">
        <h2 id="station-warroom-title">{t("station.warroom.title")}</h2>
        <span className="station-control-count" aria-label={count} title={count}>
          {live.length}
        </span>
      </div>
      {noRunner && (
        <div className="station-cta" role="status">
          <p>{t("station.runner.none")}</p>
          <button type="button" className="ap-btn is-primary" onClick={() => openSettings("runners", { add: true })}>
            {t("station.runner.add")}
          </button>
        </div>
      )}
      <Suspense fallback={<div className="station-warroom-loading">{t("station.loading")}</div>}>
        <WarRoom building={world.building} titleOf={titleOf} rowOf={rowOf} />
      </Suspense>
      <WarRoomExtras live={live} ended={ended} pilots={pilots} now={now} titleOf={titleOf} />
    </section>
  );
}
