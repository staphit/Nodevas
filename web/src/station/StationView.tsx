// Station: the project as its agents see it. Who is working, the live war
// room or boss map, who did what in the last hour, what waits on you, and
// what the agents say to each other. Info (the board) is the other face of
// the same project; the top bar switches between them.
//
// Loaded on demand (App's React.lazy), so a browser that stays in Info never
// downloads it, and the game views below it load lazily again.

import { lazy, Suspense, useEffect, useMemo, type ReactNode } from "react";

import { AgentPanel } from "../agents/AgentPanel";
import { AutopilotPanel } from "../autopilot/AutopilotPanel";
import { useAutopilot } from "../autopilot/autopilotStore";
import { setProjectMode } from "../game/stageSkin";
import { useAgents } from "../agents/agentStore";
import { agentColorKey, agentWho, poseLabel } from "../game/labels";
import { usePacks } from "../game/packStore";
import { useStationPack, useStationView } from "../game/stageSkin";
import { useWorld } from "../game/useWorld";
import type { Actor } from "../game/world";
import { useI18n } from "../i18n";
import { useBudget } from "../runs/budgetStore";
import { openNodeRuns } from "../runs/openRuns";
import { reportError, useApp } from "../store";
import { buildActivity, type ActivityLane } from "./activity";
import { useStation } from "./stationStore";
import { waitingRuns } from "./waiting";

const GameStage = lazy(() => import("../game/GameStage"));

export default function StationView({ drawer }: { drawer: ReactNode }) {
  const { t } = useI18n();
  const builtIn = useStationView();
  const pack = useStationPack();
  // A pack draws on its layout's built-in view.
  const view = pack ? (pack.layout === "rooms" ? "warroom" : "dungeon") : builtIn;
  const activeProject = useApp((s) => s.activeProject);
  const setWatching = useStation((s) => s.setWatching);

  useEffect(() => {
    setWatching(true);
    return () => setWatching(false);
  }, [setWatching]);
  useEffect(() => {
    useStation.getState().openProject(activeProject);
    void useBudget.getState().load();
    void usePacks.getState().load();
  }, [activeProject]);

  return (
    <>
      <div className="station" aria-label={t("mode.station")} role="main">
        <StationRoster />
        <div className="station-center">
          {/* Goals first: a goal opened from a node's panel is why Station
              opened, and the goals under way are what to direct. */}
          <StationGoals />
          <Suspense fallback={<div className="station-live-loading">{t("station.loading")}</div>}>
            <GameStage skin={view} pack={pack} />
          </Suspense>
          <StationActivity />
        </div>
        <div className="station-side">
          <StationWaiting />
          <AgentPanel docked />
        </div>
      </div>
      {drawer}
    </>
  );
}

const POSE_TONE: Record<Actor["pose"], string> = {
  offline: "muted",
  "asking-user": "ask",
  failed: "bad",
  "awaiting-accept": "wait",
  "waiting-ci": "wait",
  reviewing: "review",
  working: "work",
  idle: "muted",
};

/** A letter each that tells the CLIs apart (claude and codex share a C). */
const AGENT_INITIAL: Record<string, string> = {
  claude: "C",
  codex: "X",
  gemini: "G",
  opencode: "O",
  antigravity: "A",
};

function initial(agent: string): string {
  return AGENT_INITIAL[agent.toLowerCase()] ?? (agent || "?").slice(0, 1).toUpperCase();
}

function openActor(actor: Actor) {
  if (actor.nodeId) {
    if (actor.runId) openNodeRuns(actor.nodeId, actor.runId);
    else void useApp.getState().openTab(actor.nodeId).catch(reportError);
  }
}

function StationRoster() {
  const { t } = useI18n();
  const world = useWorld();
  const { actors, rooms } = world.office;
  const working = actors.filter((a) => a.pose === "working" || a.pose === "reviewing").length;
  const roomName = (id: string) => rooms.find((r) => r.id === id)?.name || t("station.roster.terminal");

  return (
    <aside className="station-panel station-roster" aria-labelledby="station-roster-title">
      <div className="station-panel-head">
        <h2 id="station-roster-title">{t("station.roster.title")}</h2>
        <span className="station-panel-meta">
          {t("station.roster.count", { working, total: actors.length })}
        </span>
      </div>
      {actors.length === 0 ? (
        <p className="station-empty">{t("station.roster.empty")}</p>
      ) : (
        <ul className="station-roster-list">
          {actors.map((actor) => (
            <li key={actor.id}>
              <button
                type="button"
                className={`station-actor tone-${POSE_TONE[actor.pose]}`}
                onClick={() => openActor(actor)}
                disabled={!actor.nodeId}
              >
                <span className={`station-avatar agent-${agentColorKey(actor.agent)}`} aria-hidden="true">
                  {initial(actor.agent)}
                </span>
                <span className="station-actor-text">
                  <span className="station-actor-name">{agentWho(actor.agent, actor.model)}</span>
                  <span className="station-actor-line">
                    <span className="station-pose">{poseLabel(t, actor.pose)}</span>
                    {actor.nodeTitle ? ` · ${actor.nodeTitle}` : ""}
                    {actor.energy ? ` · ${t("station.roster.tokens", { tokens: compact(actor.energy.used) })}` : ""}
                  </span>
                  <span className="station-actor-room">{roomName(actor.roomId)}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="station-roster-foot">
        <button
          type="button"
          className="run-link-button"
          onClick={() => window.dispatchEvent(new CustomEvent("nodevas-open-runners"))}
        >
          {t("topbar.runners")}
        </button>
      </div>
    </aside>
  );
}

function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

/**
 * Autopilot in Station: the goal a node's banner (or its menu, a party on the
 * boss map, a task waiting for you) opened, in full; else the goals under
 * way, to pick from. The goal and its tasks stay nodes of the Info graph:
 * "Show in Info" goes back to it.
 */
function StationGoals() {
  const { t } = useI18n();
  const goal = useStation((s) => s.goal);
  const focusGoal = useStation((s) => s.focusGoal);
  const views = useAutopilot((s) => s.views);
  const graph = useApp((s) => s.graph);
  const titleOf = (id: string) => graph?.nodes?.find((node) => node.id === id)?.title || id;
  const live = Object.values(views).filter(
    (view) => view.run.status === "running" || view.run.status === "paused",
  );

  if (goal) {
    return (
      <section className="station-panel station-goal" aria-labelledby="station-goal-title">
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
  if (live.length === 0) return null;
  return (
    <section className="station-panel station-goals" aria-labelledby="station-goals-title">
      <div className="station-panel-head">
        <h2 id="station-goals-title">{t("station.goals.title")}</h2>
      </div>
      <ul className="station-waiting-list">
        {live.map((view) => (
          <li key={view.run.id} className="station-waiting-item">
            <span className="station-waiting-text">
              {titleOf(view.run.goalNodeId)}
              <span className="station-waiting-sub">
                {t("station.goals.progress", { done: view.run.counts.success, total: view.run.counts.total })}
              </span>
            </span>
            <button
              type="button"
              className="run-link-button"
              onClick={() => focusGoal(view.run.goalNodeId, useApp.getState().activeProject)}
            >
              {t("station.goals.open")}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

function StationActivity() {
  const { t } = useI18n();
  const runs = useStation((s) => s.runs);
  const asks = useAgents((s) => s.asks);
  const messages = useAgents((s) => s.messages);
  const graph = useApp((s) => s.graph);
  const lanes: ActivityLane[] = useMemo(() => {
    const titles = new Map((graph?.nodes ?? []).map((node) => [node.id, node.title]));
    return buildActivity({ runs, asks, messages, now: Date.now(), titleOf: (id) => titles.get(id) ?? "" });
  }, [runs, asks, messages, graph]);

  return (
    <section className="station-panel station-activity" aria-labelledby="station-activity-title">
      <div className="station-panel-head">
        <h2 id="station-activity-title">{t("station.activity.title")}</h2>
        <ul className="station-legend" aria-label={t("station.activity.legend")}>
          <li><span className="legend-bar is-run" aria-hidden="true" />{t("station.activity.run")}</li>
          <li><span className="legend-bar is-review" aria-hidden="true" />{t("station.activity.review")}</li>
          <li><span className="legend-mark is-ask" aria-hidden="true" />{t("station.activity.ask")}</li>
          <li><span className="legend-mark is-message" aria-hidden="true" />{t("station.activity.message")}</li>
          <li><span className="legend-mark is-limit" aria-hidden="true" />{t("station.activity.limit")}</li>
        </ul>
      </div>
      {lanes.length === 0 ? (
        <p className="station-empty">{t("station.activity.empty")}</p>
      ) : (
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
      )}
    </section>
  );
}

function StationWaiting() {
  const { t } = useI18n();
  const runs = useStation((s) => s.runs);
  const budget = useBudget((s) => s.status);
  const graph = useApp((s) => s.graph);
  const items = useMemo(() => waitingRuns(runs, Date.now()), [runs]);
  const titleOf = (id: string) => graph?.nodes?.find((node) => node.id === id)?.title || id;
  const exceeded = budget?.exceeded;
  if (items.length === 0 && !exceeded) return null;

  return (
    <section className="station-panel station-waiting" aria-labelledby="station-waiting-title">
      <div className="station-panel-head">
        <h2 id="station-waiting-title">{t("station.waiting.title")}</h2>
        <span className="station-count">{items.length + (exceeded ? 1 : 0)}</span>
      </div>
      <ul className="station-waiting-list">
        {exceeded && (
          <li className="station-waiting-item is-budget">
            <span className="station-waiting-text">
              {t("budget.exceeded", { limit: t(`budget.limit.${exceeded}`) })}
            </span>
            <button
              type="button"
              className="run-link-button"
              onClick={() => window.dispatchEvent(new CustomEvent("nodevas-open-budget"))}
            >
              {t("budget.adjust")}
            </button>
          </li>
        )}
        {items.map(({ kind, run, tookOver }) => (
          <li key={`${kind}-${run.id}`} className={`station-waiting-item is-${kind}`}>
            <span className="station-waiting-text">
              {kind === "limit"
                ? t("station.waiting.limit", { agent: agentWho(run.agent, run.model), name: titleOf(run.nodeId) })
                : t("station.waiting.merge", { number: run.prNumber ?? "", name: titleOf(run.nodeId) })}
              {kind === "limit" && tookOver && (
                <span className="station-waiting-sub">
                  {t("station.waiting.tookOver", { agent: agentWho(tookOver.agent, tookOver.model) })}
                </span>
              )}
            </span>
            <button type="button" className="run-link-button" onClick={() => openNodeRuns(run.nodeId, run.id)}>
              {kind === "limit" ? t("station.waiting.openRun") : t("station.waiting.review")}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
