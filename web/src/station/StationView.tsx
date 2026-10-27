// Station: the project as its agents see it. What waits on you comes first;
// then the war room, where what is under way sits at its desks, with one
// emergency stop in the header. The side column says what today cost,
// which runners there are and what the agents say to each other, each folded
// to a line until asked. Info (the board) is the other face of the same
// project; the top bar switches between them.
//
// Loaded on demand (App's React.lazy), so a browser that stays in Info never
// downloads it, and the war room's drawing below it loads lazily again.

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { useAgents } from "../agents/agentStore";
import { AutopilotPanel } from "../autopilot/AutopilotPanel";
import { GameActionsProvider, useGameActions } from "../game/GameActions";
import { setProjectMode } from "./projectMode";
import { useWorld } from "../game/useWorld";
import { useI18n } from "../i18n";
import { useBudget } from "../runs/budgetStore";
import { openNodeRuns } from "../runs/openRuns";
import { useRuns } from "../runs/runStore";
import { reportError, useApp } from "../store";
import { buildActivity, type ActivityLane } from "./activity";
import { useBuilding } from "./buildingStore";
import { StationControlReport, StationPauseButton } from "./StationControl";
import { StationSide } from "./StationSide";
import { useStation } from "./stationStore";
import { StationWaiting } from "./StationWaiting";
import { StationWarRoom } from "./StationWarRoom";

export default function StationView({ drawer }: { drawer: ReactNode }) {
  const { t } = useI18n();
  const activeProject = useApp((s) => s.activeProject);
  const setWatching = useStation((s) => s.setWatching);
  const world = useWorld();

  useEffect(() => {
    setWatching(true);
    useBuilding.getState().setWatching(true);
    return () => {
      setWatching(false);
      useBuilding.getState().setWatching(false);
    };
  }, [setWatching]);
  const firstProject = useRef(true);
  useEffect(() => {
    useStation.getState().openProject(activeProject);
    void useBudget.getState().load();
    // The mount's load is setWatching's; a switch reloads (the current
    // office leaves the building summary, the old one joins it).
    if (firstProject.current) firstProject.current = false;
    else void useBuilding.getState().load();
  }, [activeProject]);

  return (
    <GameActionsProvider>
      <DeferredTarget />
      <div className="station" aria-label={t("mode.station")} role="main">
        <header className="station-head">
          <h2>{t("mode.station")}</h2>
          <StationPauseButton />
        </header>
        <div className="station-report">
          <StationControlReport />
        </div>
        <div className="station-center">
          <StationWaiting />
          {/* A goal opened from a node's panel (or a review waiting on you)
              is why Station opened: right under what waits on you. */}
          <StationGoal />
          <StationWarRoom world={world} />
          <StationActivity />
        </div>
        <StationSide world={world} />
      </div>
      {drawer}
    </GameActionsProvider>
  );
}

/** Something clicked in another project's office: once Station has
 * switched there and what it needs has loaded, it is done (and forgotten). */
function DeferredTarget() {
  const { dispatch } = useGameActions();
  const deferred = useStation((s) => s.deferred);
  const activeProject = useApp((s) => s.activeProject);
  const graph = useApp((s) => s.graph);
  const runsLoaded = useRuns((s) => s.activeRunsLoaded);
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  useEffect(() => {
    if (!deferred?.ready || deferred.project !== activeProject || !graph) return;
    const target = deferred.target;
    if (target.kind === "run" && !runsLoaded) return;
    if (target.kind === "ask" && !asks.some((ask) => ask.id === target.askId)) return;
    if (target.kind === "held-message" && !held.some((message) => message.id === target.messageId)) return;
    useStation.getState().clearDeferred();
    dispatch(target);
  }, [deferred, activeProject, graph, runsLoaded, asks, held, dispatch]);
  return null;
}

/**
 * Autopilot in Station: the goal a node's banner (or its menu, a task waiting
 * for you, a war-room character or tile) opened, in full.
 * The goal and its tasks stay nodes of the board: "Show on board" goes
 * back to it.
 */
function StationGoal() {
  const { t } = useI18n();
  const goal = useStation((s) => s.goal);
  const focusGoal = useStation((s) => s.focusGoal);
  const graph = useApp((s) => s.graph);
  const ref = useRef<HTMLElement | null>(null);
  const titleOf = (id: string) => graph?.nodes?.find((node) => node.id === id)?.title || id;

  // Opened from further down the page: bring it into view.
  useEffect(() => {
    if (goal) ref.current?.scrollIntoView?.({ block: "nearest", behavior: "smooth" });
  }, [goal]);

  if (!goal) return null;
  return (
    <section ref={ref} className="station-panel station-goal" aria-labelledby="station-goal-title">
      <div className="station-panel-head">
        <h2 id="station-goal-title">{t("station.goal.title", { name: titleOf(goal) })}</h2>
        <button
          type="button"
          className="run-link-button"
          onClick={() => {
            setProjectMode("info");
            void useApp.getState().openTab(goal).catch(reportError);
          }}
        >
          {t("station.goal.showInInfo")}
        </button>
        <button
          type="button"
          className="run-link-button"
          onClick={() => focusGoal(null)}
          aria-label={t("station.goal.close")}
        >
          ✕
        </button>
      </div>
      <AutopilotPanel id={goal} />
    </section>
  );
}

/** Who worked on what in the last hour: folded under its heading. */
function StationActivity() {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <section className="station-panel station-activity" aria-labelledby="station-activity-title">
      <div className="station-panel-head">
        <h2 id="station-activity-title">
          <button
            type="button"
            className="station-fold is-heading"
            aria-expanded={open}
            aria-controls={open ? "station-activity-body" : undefined}
            onClick={() => setOpen((was) => !was)}
          >
            <span aria-hidden="true">{open ? "▾" : "▸"} </span>
            {t("station.activity.title")}
          </button>
        </h2>
        {open && <ActivityLegend />}
      </div>
      {open && (
        <div id="station-activity-body">
          <ActivityLanes />
        </div>
      )}
    </section>
  );
}

function ActivityLegend() {
  const { t } = useI18n();
  return (
    <ul className="station-legend" aria-label={t("station.activity.legend")}>
      <li><span className="legend-bar is-run" aria-hidden="true" />{t("station.activity.run")}</li>
      <li><span className="legend-bar is-review" aria-hidden="true" />{t("station.activity.review")}</li>
      <li><span className="legend-mark is-ask" aria-hidden="true" />{t("station.activity.ask")}</li>
      <li><span className="legend-mark is-message" aria-hidden="true" />{t("station.activity.message")}</li>
      <li><span className="legend-mark is-limit" aria-hidden="true" />{t("station.activity.limit")}</li>
    </ul>
  );
}

function ActivityLanes() {
  const { t } = useI18n();
  const runs = useStation((s) => s.runs);
  const asks = useAgents((s) => s.asks);
  const messages = useAgents((s) => s.messages);
  const graph = useApp((s) => s.graph);
  const lanes: ActivityLane[] = useMemo(() => {
    const titles = new Map((graph?.nodes ?? []).map((node) => [node.id, node.title]));
    return buildActivity({ runs, asks, messages, now: Date.now(), titleOf: (id) => titles.get(id) ?? "" });
  }, [runs, asks, messages, graph]);

  if (lanes.length === 0) return <p className="station-empty">{t("station.activity.empty")}</p>;
  return (
    <div className="station-lanes">
      {lanes.map((lane) => (
        <div key={lane.key} className="station-lane">
          <span className="station-lane-name">{lane.label}</span>
          <div className="station-lane-track">
            {lane.bars.map((bar) => (
              <button
                key={bar.runId}
                type="button"
                className={`station-bar is-${bar.kind} status-${bar.status}${bar.open ? " is-open" : ""}`}
                style={{ left: `${bar.start * 100}%`, width: `${Math.max(bar.end - bar.start, 0.012) * 100}%` }}
                title={`${bar.label} · ${t(`runs.status.${bar.status}`)}`}
                onClick={() => openNodeRuns(bar.nodeId, bar.runId)}
              >
                <span>{bar.label}</span>
              </button>
            ))}
            {lane.marks.map((mark, index) => (
              <span
                key={`${mark.kind}-${index}`}
                className={`station-mark is-${mark.kind}`}
                style={{ left: `${mark.at * 100}%` }}
                title={mark.text}
                role="img"
                aria-label={`${t(`station.activity.${mark.kind}`)}: ${mark.text}`}
              />
            ))}
          </div>
        </div>
      ))}
      <div className="station-lane station-axis" aria-hidden="true">
        <span />
        <div className="station-axis-ticks">
          <span>{t("station.activity.minutesAgo", { n: 60 })}</span>
          <span>{t("station.activity.minutesAgo", { n: 45 })}</span>
          <span>{t("station.activity.minutesAgo", { n: 30 })}</span>
          <span>{t("station.activity.minutesAgo", { n: 15 })}</span>
          <span>{t("station.activity.now")}</span>
        </div>
      </div>
    </div>
  );
}
