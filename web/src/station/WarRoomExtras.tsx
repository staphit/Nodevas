// Under the war room: what the room does not draw. The live autopilots (a
// goal can be under way with nobody at a desk for it), the tasks waiting on
// prerequisites, and the runs that ended in the last hour, each as tiles,
// the last two folded to one line until asked.

import { useMemo, useState } from "react";

import type { AutopilotView } from "../api";
import { AutopilotStatusChip } from "../autopilot/AutopilotPanel";
import { analyzeGraph } from "../analysis";
import { useViewer } from "../components/SignIn";
import { IconMore } from "../icons";
import { useI18n } from "../i18n";
import { RunStatusBadge } from "../runs/NodeRuns";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { canSteer, type ControlRow } from "./control";
import { waitingOnPrerequisites } from "./prerequisites";
import { AutopilotMenu, openNode, RunMenu, runMeta, useAutopilotError } from "./runMenuItems";
import { StationTile, StationTiles } from "./StationTile";

type TitleOf = (id: string) => string;

export function WarRoomExtras({
  live,
  ended,
  pilots,
  now,
  titleOf,
}: {
  live: ControlRow[];
  ended: ControlRow[];
  pilots: AutopilotView[];
  now: number;
  titleOf: TitleOf;
}) {
  const { t } = useI18n();
  return (
    <div className="station-warroom-extras">
      <CancelErrors live={live} />
      {pilots.length > 0 && (
        <StationTiles label={t("station.warroom.autopilots")}>
          {pilots.map((view) => (
            <AutopilotTile key={view.run.id} view={view} title={titleOf(view.run.goalNodeId)} />
          ))}
        </StationTiles>
      )}
      <WaitingTasks titleOf={titleOf} />
      <EndedRuns ended={ended} now={now} titleOf={titleOf} />
    </div>
  );
}

/** A cancel that failed, said under the room: the menu it came from has
 * closed. */
function CancelErrors({ live }: { live: ControlRow[] }) {
  const errors = useRuns((s) => s.cancelErrors);
  const shown = live.filter((row) => errors[row.run.id]);
  if (shown.length === 0) return null;
  return (
    <>
      {shown.map((row) => (
        <p key={row.run.id} className="autopilot-error" role="alert">
          {errors[row.run.id]}
        </p>
      ))}
    </>
  );
}

function AutopilotTile({ view, title }: { view: AutopilotView; title: string }) {
  const { t } = useI18n();
  const error = useAutopilotError(view);
  const viewer = useViewer();
  const { success, total } = view.run.counts;
  const progress = t("station.goals.progress", { done: success, total });
  // Someone else's autopilot has no pause or cancel here: say whose it is.
  const meta = canSteer(viewer, view.run.createdBy)
    ? progress
    : `${progress} · ${t("station.control.startedBy", { name: view.run.createdByName || view.run.createdBy })}`;
  const label = t("station.progress.actions", { name: title });
  return (
    <StationTile
      dot={view.run.status === "paused" ? "paused" : "running"}
      title={title}
      nodeId={view.run.goalNodeId}
      meta={meta}
      badge={<AutopilotStatusChip status={view.run.status} />}
      action={
        <AutopilotMenu
          view={view}
          title={title}
          label={label}
          trigger={<IconMore size={16} />}
          triggerClassName="station-menu-trigger is-icon"
        />
      }
    >
      <progress className="station-progress-bar" max={Math.max(total, 1)} value={success} aria-label={progress} />
      {error && (
        <span className="autopilot-error" role="alert">
          {error}
        </span>
      )}
    </StationTile>
  );
}

/** A line that opens what is under it. */
function Fold({ label, open, onToggle }: { label: string; open: boolean; onToggle: () => void }) {
  return (
    <button type="button" className="station-fold" aria-expanded={open} onClick={onToggle}>
      <span aria-hidden="true">{open ? "▾" : "▸"}</span> {label}
    </button>
  );
}

/** The tasks waiting on others to finish, as one line until asked. */
function WaitingTasks({ titleOf }: { titleOf: TitleOf }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const statuses = useApp((s) => s.statuses);
  const [open, setOpen] = useState(false);
  const tasks = useMemo(
    () => waitingOnPrerequisites(graph, statuses, analyzeGraph(graph, statuses)),
    [graph, statuses],
  );
  if (tasks.length === 0) return null;
  const label = t("station.progress.locked", { count: tasks.length });
  return (
    <div className="station-warroom-fold">
      <Fold label={label} open={open} onToggle={() => setOpen((was) => !was)} />
      {open && (
        <StationTiles label={label} className="is-muted">
          {tasks.map((task) => (
            <StationTile
              key={task.nodeId}
              dot="off"
              title={task.title || titleOf(task.nodeId)}
              nodeId={task.nodeId}
              meta={t("station.progress.lockedBy", { names: task.blockers.map(titleOf).join(", ") })}
              onOpen={() => openNode(task.nodeId)}
            />
          ))}
        </StationTiles>
      )}
    </div>
  );
}

/** Ended in the last hour: folded away, as tiles with the same menu. */
function EndedRuns({ ended, now, titleOf }: { ended: ControlRow[]; now: number; titleOf: TitleOf }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  if (ended.length === 0) return null;
  const label = t("station.control.ended", { count: ended.length });
  return (
    <div className="station-warroom-fold">
      <Fold label={label} open={open} onToggle={() => setOpen((was) => !was)} />
      {open && (
        <StationTiles label={label} className="is-muted">
          {ended.map((row) => {
            const title = titleOf(row.run.nodeId);
            const goal = row.autopilot ? titleOf(row.autopilot.run.goalNodeId) : "";
            return (
              <StationTile
                key={row.run.id}
                dot={row.run.status}
                title={title}
                nodeId={row.run.nodeId}
                meta={runMeta(t, row.run, now, goal)}
                badge={<RunStatusBadge status={row.run.status} />}
                action={
                  <RunMenu
                    row={row}
                    titleOf={titleOf}
                    label={t("station.progress.actions", { name: title })}
                    trigger={<IconMore size={16} />}
                    triggerClassName="station-menu-trigger is-icon"
                  />
                }
              />
            );
          })}
        </StationTiles>
      )}
    </div>
  );
}
