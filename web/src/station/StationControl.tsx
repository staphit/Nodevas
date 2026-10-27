// Station's control center: every run under way in the project, the
// autopilots driving some of them, and one button that stops them all. Each
// control is the one the run's or the goal's own view offers (same question,
// same store action); this only puts them in one place. A row shows what it
// is and keeps its actions in one ⋯ menu; the header's view menu swaps the
// list for the war room or the boss map.

import { Suspense, useEffect, useId, useMemo, useRef, useState } from "react";

import { runCancellable, runFinal, type AutopilotView, type Run } from "../api";
import { AutopilotStatusChip } from "../autopilot/AutopilotPanel";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useAutopilotControl } from "../autopilot/useAutopilotControl";
import { confirmAction } from "../components/ConfirmDialog";
import { useViewer } from "../components/SignIn";
import { useGameActions } from "../game/GameActions";
import { agentWho } from "../game/labels";
import { packName, usePacks } from "../game/packStore";
import {
  setStageSkin,
  setStationPack,
  STAGE_SKIN_LABEL,
  STATION_VIEWS,
  useStationPack,
  useStationView,
} from "../game/stageSkin";
import type { World } from "../game/world";
import { IconMore, IconWarning } from "../icons";
import { useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { confirmCancelRun } from "../runs/confirmCancelRun";
import { RunStatusBadge } from "../runs/NodeRuns";
import { openNodeRuns } from "../runs/openRuns";
import { formatCompact, formatCost, totalTokens } from "../runs/runFormat";
import { useRuns } from "../runs/runStore";
import { openSettings } from "../settings/openSettings";
import { lazyWithRetry } from "../lazyWithRetry";
import { reportError, useApp } from "../store";
import {
  canSteer,
  controlRows,
  controlRunKind,
  elapsedMs,
  formatElapsed,
  liveAutopilots,
  pauseAllPlan,
  type ControlRow,
  type PauseAllPlan,
} from "./control";
import { useControl } from "./controlStore";
import { StationMenu, type StationMenuItem } from "./StationMenu";
import { useStation } from "./stationStore";

const GameStage = lazyWithRetry(() => import("../game/GameStage"));

/** Now, again every `ms` while mounted: the elapsed column counts up. */
function useNow(ms: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(timer);
  }, [ms]);
  return now;
}

export function useTitleOf(): (id: string) => string {
  const graph = useApp((s) => s.graph);
  return useMemo(() => {
    const titles = new Map(
      (graph?.nodes ?? []).map((node) => [node.id, node.title]),
    );
    return (id: string) => titles.get(id) || id;
  }, [graph]);
}

function openNode(nodeId: string) {
  void useApp.getState().openTab(nodeId).catch(reportError);
}

function openGoal(goalNodeId: string) {
  useStation.getState().focusGoal(goalNodeId, useApp.getState().activeProject);
}

/** The active runs and live autopilots, asked for again whenever a project
 * switch (RunsRoot) resets their stores, maybe after Station mounted. */
function useControlLoads() {
  const activeProject = useApp((s) => s.activeProject);
  const activeRunsLoaded = useRuns((s) => s.activeRunsLoaded);
  const autopilotsLoaded = useAutopilot((s) => s.activeLoaded);
  useEffect(() => {
    if (!activeRunsLoaded) void useRuns.getState().loadActiveRuns();
  }, [activeRunsLoaded, activeProject]);
  useEffect(() => {
    if (!autopilotsLoaded) void useAutopilot.getState().loadActive();
  }, [autopilotsLoaded, activeProject]);
  // The last pause-all's report belongs to the project it stopped.
  useEffect(() => () => useControl.getState().dismiss(), [activeProject]);
}

/**
 * Station's one emergency button, in its header: "Pause everything" while
 * anything can be paused or withdrawn (asking first, saying exactly what),
 * "Resume all" once only the viewer's paused autopilots are left.
 */
export function StationPauseButton() {
  const { t } = useI18n();
  const viewer = useViewer();
  const active = useRuns((s) => s.activeRuns);
  const views = useAutopilot((s) => s.views);
  const pausing = useControl((s) => s.pausing);
  const resumeAll = useControl((s) => s.resumeAll);
  const [confirming, setConfirming] = useState(false);
  const plan = useMemo(() => pauseAllPlan(viewer, active, views), [viewer, active, views]);
  const anything = plan.pause.length + plan.pending.length + plan.running.length > 0;
  const paused = useMemo(
    () => liveAutopilots(views).filter((view) => view.run.status === "paused" && canSteer(viewer, view.run.createdBy)),
    [views, viewer],
  );

  if (!anything && paused.length > 0) {
    const onResume = async () => {
      const ok = await confirmAction({
        title: t("station.control.resumeAllTitle"),
        description: t("station.control.resumeAllIntro", { count: paused.length }),
        confirmLabel: t("station.control.resumeAll"),
      });
      if (ok) await resumeAll(paused.map((view) => view.run.id));
    };
    return (
      <button type="button" className="ap-btn station-head-stop" disabled={pausing} onClick={() => void onResume()}>
        {t("station.control.resumeAll")}
      </button>
    );
  }
  return (
    <>
      <button
        type="button"
        className="ap-btn is-danger station-head-stop"
        disabled={!anything}
        onClick={() => setConfirming(true)}
      >
        <IconWarning size={13} /> {t("station.control.pauseAll")}
      </button>
      {confirming && <PauseAllDialog plan={plan} onClose={() => setConfirming(false)} />}
    </>
  );
}

/** What the last pause-all did (or why it failed), under Station's header. */
export function StationControlReport() {
  const { t } = useI18n();
  const result = useControl((s) => s.result);
  const error = useControl((s) => s.error);
  const dismiss = useControl((s) => s.dismiss);
  return (
    <>
      {error && (
        <p className="autopilot-error" role="alert">
          {error}
        </p>
      )}
      {result && (
        <p className="station-control-result" role="status">
          {t("station.control.result", {
            paused: result.paused.length,
            cancelled: result.cancelled.length,
            skipped: result.skipped.length,
          })}
          <button type="button" className="run-link-button" onClick={dismiss} aria-label={t("common.close")}>
            ✕
          </button>
        </p>
      )}
    </>
  );
}

/**
 * "In progress": the runs and autopilots under way as a compact list, or the
 * war room or boss map drawing the same, picked from one view menu. With no
 * runner and nothing under way, the list is one call to add a runner; a game
 * view picked on purpose still draws (the boss map is the board's tasks).
 */
export function StationControl({ world }: { world?: World }) {
  const { language, t } = useI18n();
  useControlLoads();
  const active = useRuns((s) => s.activeRuns);
  const recent = useStation((s) => s.runs);
  const views = useAutopilot((s) => s.views);
  const liveView = useStation((s) => s.liveView);
  const setLiveView = useStation((s) => s.setLiveView);
  const builtIn = useStationView();
  const pack = useStationPack();
  const packs = usePacks((s) => s.packs);
  const [textView, setTextView] = useState(false);
  const now = useNow(1000);
  const titleOf = useTitleOf();

  const { live, ended } = useMemo(() => controlRows(active, recent, views, now), [active, recent, views, now]);
  const pilots = useMemo(() => liveAutopilots(views), [views]);
  const noRunner = useNoRunner(live.length + pilots.length > 0);

  // A pack draws on its layout's built-in view.
  const skin = pack ? (pack.layout === "rooms" ? "warroom" : "dungeon") : builtIn;
  const game = liveView === "game";
  const viewName = !game ? t("station.view.list") : pack ? packName(pack, language) : t(STAGE_SKIN_LABEL[skin]);
  const viewItems: StationMenuItem[] = [
    {
      id: "list",
      label: t("station.view.list"),
      selected: !game,
      onSelect: () => setLiveView("list"),
    },
    ...STATION_VIEWS.map((view) => ({
      id: view,
      label: t(STAGE_SKIN_LABEL[view]),
      selected: game && !pack && builtIn === view,
      onSelect: () => setStageSkin(view),
    })),
    ...packs.map((item) => ({
      id: `pack-${item.id}`,
      label: packName(item, language),
      title: t("packs.switchTitle"),
      selected: game && pack?.id === item.id,
      onSelect: () => setStationPack(item),
    })),
  ];
  if (game) {
    viewItems.push({
      id: "text",
      label: t("station.view.asText"),
      checked: textView,
      separatorBefore: true,
      onSelect: () => setTextView((on) => !on),
    });
  }

  return (
    <section className="station-panel station-control" aria-labelledby="station-control-title">
      <div className="station-panel-head">
        <h2 id="station-control-title">{t("station.progress.title")}</h2>
        <span
          className="station-control-count"
          aria-label={t("station.control.activeCount", { count: live.length })}
          title={t("station.control.activeCount", { count: live.length })}
        >
          {live.length}
        </span>
        <span className="station-head-gap" />
        <StationMenu
          label={t("station.view.current", { name: viewName })}
          trigger={
            <>
              {t("station.view.current", { name: viewName })} <span aria-hidden="true">▾</span>
            </>
          }
          triggerClassName="station-menu-trigger is-text"
          items={viewItems}
        />
      </div>

      {game ? (
        <Suspense fallback={<div className="station-live-loading">{t("station.loading")}</div>}>
          <GameStage skin={skin} pack={pack} controls={false} listView={textView} />
        </Suspense>
      ) : noRunner ? (
        <div className="station-cta" role="status">
          <p>{t("station.runner.none")}</p>
          <button type="button" className="ap-btn is-primary" onClick={() => openSettings("runners", { add: true })}>
            {t("station.runner.add")}
          </button>
        </div>
      ) : (
        <ControlList live={live} ended={ended} pilots={pilots} now={now} titleOf={titleOf} world={world} />
      )}
    </section>
  );
}

/** No runner at all, nothing under way and no agent online: Station has
 * nothing to show but the way to start. Unknown (still loading, or the
 * viewer may not list runners) is not "none". */
function useNoRunner(busy: boolean): boolean {
  const loaded = useRunners((s) => s.loaded);
  const loading = useRunners((s) => s.loading);
  const error = useRunners((s) => s.error);
  const count = useRunners((s) => s.runners.length);
  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);
  return loaded && !loading && !error && count === 0 && !busy;
}

function ControlList({
  live,
  ended,
  pilots,
  now,
  titleOf,
  world,
}: {
  live: ControlRow[];
  ended: ControlRow[];
  pilots: AutopilotView[];
  now: number;
  titleOf: (id: string) => string;
  world?: World;
}) {
  const { t } = useI18n();
  const [showEnded, setShowEnded] = useState(false);

  return (
    <>
      {live.length + pilots.length === 0 && <p className="station-empty">{t("station.control.empty")}</p>}
      {live.length + pilots.length > 0 && (
        <ul className="station-progress-list" aria-label={t("station.progress.title")}>
          {live.map((row) => (
            <ControlRunRow key={row.run.id} row={row} now={now} titleOf={titleOf} />
          ))}
          {pilots.map((view) => (
            <AutopilotItem key={view.run.id} view={view} title={titleOf(view.run.goalNodeId)} />
          ))}
        </ul>
      )}
      {/* Ended in the last hour: folded away, in the same rows. */}
      {ended.length > 0 && (
        <>
          <button
            type="button"
            className="station-fold"
            aria-expanded={showEnded}
            onClick={() => setShowEnded((open) => !open)}
          >
            <span aria-hidden="true">{showEnded ? "▾" : "▸"}</span> {t("station.control.ended", { count: ended.length })}
          </button>
          {showEnded && (
            <ul className="station-progress-list is-ended" aria-label={t("station.control.ended", { count: ended.length })}>
              {ended.map((row) => (
                <ControlRunRow key={row.run.id} row={row} now={now} titleOf={titleOf} />
              ))}
            </ul>
          )}
        </>
      )}
      {world && <LockedTasks world={world} titleOf={titleOf} />}
    </>
  );
}

/** The tasks waiting on others to finish, as one line until asked. */
function LockedTasks({ world, titleOf }: { world: World; titleOf: (id: string) => string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const locked = world.dungeon.monsters.filter((monster) => monster.state === "locked");
  if (locked.length === 0) return null;
  return (
    <div className="station-locked">
      <div className="station-locked-line">
        <span className="station-dot is-off" aria-hidden="true" />
        <span className="station-progress-meta">{t("station.progress.locked", { count: locked.length })}</span>
        <button type="button" className="run-link-button" aria-expanded={open} onClick={() => setOpen((was) => !was)}>
          {open ? t("station.progress.collapse") : t("station.progress.expand")}
        </button>
      </div>
      {open && (
        <ul className="station-progress-list is-locked">
          {locked.map((monster) => (
            <li key={monster.nodeId} className="station-progress-row">
              <span className="station-progress-text">
                <button type="button" className="run-link-button station-progress-title" onClick={() => openNode(monster.nodeId)}>
                  {monster.title || titleOf(monster.nodeId)}
                </button>
                <span className="station-progress-meta">
                  {t("station.progress.lockedBy", { names: monster.blockers.map(titleOf).join(", ") })}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ControlRunRow({ row, now, titleOf }: { row: ControlRow; now: number; titleOf: (id: string) => string }) {
  const viewer = useViewer();
  const autopilot = row.autopilot;
  if (autopilot && canSteer(viewer, autopilot.run.createdBy)) {
    return <SteeredRunRow row={row} autopilot={autopilot} now={now} titleOf={titleOf} />;
  }
  return <RunRowView row={row} now={now} titleOf={titleOf} steer={[]} />;
}

/** A run that is a step of an autopilot the viewer may steer: its menu
 * also pauses or resumes that autopilot. */
function SteeredRunRow({
  row,
  autopilot,
  now,
  titleOf,
}: {
  row: ControlRow;
  autopilot: AutopilotView;
  now: number;
  titleOf: (id: string) => string;
}) {
  return <RunRowView row={row} now={now} titleOf={titleOf} steer={useSteerItems(autopilot, false)} />;
}

/** Pause or resume (adopting a revised goal) an autopilot, and with
 * `withCancel` cancel it: the goal panel's own controls, as menu items. */
function useSteerItems(view: AutopilotView, withCancel: boolean): StationMenuItem[] {
  const { t } = useI18n();
  const { acting, resumeAction, resumeLabel, control } = useAutopilotControl(view);
  const status = view.run.status;
  const items: StationMenuItem[] = [];
  if (status === "running") {
    items.push({ id: "pause", label: t("station.control.pauseGoal"), disabled: acting, onSelect: () => void control("pause") });
  }
  if (status === "paused") {
    items.push({ id: "resume", label: resumeLabel, disabled: acting, onSelect: () => void control(resumeAction) });
  }
  if (withCancel && (status === "running" || status === "paused")) {
    items.push({ id: "cancel", label: t("autopilot.cancel"), danger: true, disabled: acting, onSelect: () => void control("cancel") });
  }
  return items;
}

function RunRowView({
  row,
  now,
  titleOf,
  steer,
}: {
  row: ControlRow;
  now: number;
  titleOf: (id: string) => string;
  steer: StationMenuItem[];
}) {
  const { t } = useI18n();
  const { dispatch } = useGameActions();
  const { run, autopilot } = row;
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const stopping = useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelRun = useRuns((s) => s.cancelRun);
  const title = titleOf(run.nodeId);

  const onCancel = async () => {
    if (await confirmCancelRun(t, run)) await cancelRun(run.id);
  };

  const items: StationMenuItem[] = [
    { id: "run", label: t("station.control.openRun"), onSelect: () => openNodeRuns(run.nodeId, run.id) },
    {
      id: "diff",
      label: t("game.action.runDiff"),
      disabled: !run.commit,
      title: run.commit ? undefined : t("station.progress.noDiff"),
      onSelect: () => dispatch({ kind: "run-diff", runId: run.id, nodeId: run.nodeId }),
    },
    { id: "node", label: t("game.action.node", { title }), onSelect: () => openNode(run.nodeId) },
  ];
  if (autopilot) {
    items.push({
      id: "goal",
      label: t("station.progress.openGoal", { name: titleOf(autopilot.run.goalNodeId) }),
      onSelect: () => openGoal(autopilot.run.goalNodeId),
    });
  }
  items.push(...steer.map((item, index) => ({ ...item, separatorBefore: index === 0 })));
  if (runCancellable(run.status) && !stopping) {
    items.push({
      id: "cancel",
      label: cancelling ? t("runs.cancelling") : t("runs.cancel"),
      danger: true,
      disabled: cancelling,
      separatorBefore: true,
      onSelect: () => void onCancel(),
    });
  }

  return (
    <li className={`station-progress-row status-${run.status}`}>
      <span className={`station-dot status-${run.status}`} aria-hidden="true" />
      <span className="station-progress-text">
        <span className="station-progress-title">{title}</span>
        <span className="station-progress-meta">{runMeta(t, run, now, autopilot ? titleOf(autopilot.run.goalNodeId) : "")}</span>
        {cancelError && (
          <span className="autopilot-error" role="alert">
            {cancelError}
          </span>
        )}
      </span>
      {stopping ? (
        <span className="station-control-note">{t("station.control.stopping")}</span>
      ) : (
        run.status !== "running" && <RunStatusBadge status={run.status} />
      )}
      <StationMenu
        label={t("station.progress.actions", { name: title })}
        trigger={<IconMore size={16} />}
        triggerClassName="station-menu-trigger is-icon"
        items={items}
      />
    </li>
  );
}

/** "Review · Codex gpt-5 · laptop · 4m 05s · 12.3K tokens · $0.042 · Ship login". */
function runMeta(t: ReturnType<typeof useI18n>["t"], run: Run, now: number, goal: string): string {
  const tokens = totalTokens(run.usage);
  const parts = [
    t(`station.control.kind.${controlRunKind(run)}`),
    agentWho(run.agent, run.model),
    run.runnerName,
    formatElapsed(elapsedMs(run, now)),
    tokens > 0 ? t("station.control.tokens", { tokens: formatCompact(tokens) }) : "",
    formatCost(run.usage?.costUsd),
    goal,
  ];
  return parts.filter(Boolean).join(" · ");
}

function AutopilotItem({ view, title }: { view: AutopilotView; title: string }) {
  const viewer = useViewer();
  if (canSteer(viewer, view.run.createdBy)) return <SteeredAutopilotItem view={view} title={title} />;
  return <AutopilotItemView view={view} title={title} steer={null} />;
}

function SteeredAutopilotItem({ view, title }: { view: AutopilotView; title: string }) {
  return <AutopilotItemView view={view} title={title} steer={useSteerItems(view, true)} />;
}

function AutopilotItemView({
  view,
  title,
  steer,
}: {
  view: AutopilotView;
  title: string;
  /** null: someone else's autopilot, which the viewer may not steer. */
  steer: StationMenuItem[] | null;
}) {
  const { t } = useI18n();
  const error = useAutopilot((s) => s.errors[view.run.goalNodeId] ?? "");
  const { success, total } = view.run.counts;
  const progress = t("station.goals.progress", { done: success, total });
  const items: StationMenuItem[] = [
    { id: "goal", label: t("station.progress.openGoal", { name: title }), onSelect: () => openGoal(view.run.goalNodeId) },
    ...(steer ?? []).map((item, index) => ({ ...item, separatorBefore: index === 0 })),
  ];
  return (
    <li className={`station-progress-row is-autopilot status-${view.run.status}`}>
      <span className={`station-dot status-${view.run.status === "paused" ? "paused" : "running"}`} aria-hidden="true" />
      <span className="station-progress-text">
        <span className="station-progress-title">{title}</span>
        <span className="station-progress-meta">
          {steer
            ? progress
            : `${progress} · ${t("station.control.startedBy", { name: view.run.createdByName || view.run.createdBy })}`}
        </span>
        <progress className="station-progress-bar" max={Math.max(total, 1)} value={success} aria-label={progress} />
        {error && (
          <span className="autopilot-error" role="alert">
            {error}
          </span>
        )}
      </span>
      <AutopilotStatusChip status={view.run.status} />
      <StationMenu
        label={t("station.progress.actions", { name: title })}
        trigger={<IconMore size={16} />}
        triggerClassName="station-menu-trigger is-icon"
        items={items}
      />
    </li>
  );
}

/** "Pause everything": exactly what will happen, counted, and whether the
 * runs already running go too (off unless ticked). */
export function PauseAllDialog({
  plan,
  onClose,
}: {
  plan: PauseAllPlan;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const pauseAll = useControl((s) => s.pauseAll);
  const pausing = useControl((s) => s.pausing);
  const [cancelRunning, setCancelRunning] = useState(false);
  const keepRef = useRef<HTMLButtonElement>(null);
  const titleId = useId();
  const descriptionId = useId();

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    keepRef.current?.focus();
    // Captured on the document and claimed, so the drawer or board beneath
    // does not also read this Escape as its own.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      onClose();
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      previous?.focus?.();
    };
  }, [onClose]);

  const confirm = async () => {
    if (await pauseAll(cancelRunning)) onClose();
  };

  return (
    <div
      className="confirm-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        className="confirm-dialog danger station-control-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <header>
          <span className="confirm-dialog-icon">
            <IconWarning size={17} />
          </span>
          <div>
            <h2 id={titleId}>{t("station.control.pauseAllTitle")}</h2>
            <p id={descriptionId}>{t("station.control.pauseAllIntro")}</p>
          </div>
        </header>
        <div className="station-control-dialog-body">
          <ul>
            <li>
              {t("station.control.plan.pause", { count: plan.pause.length })}
            </li>
            <li>
              {t("station.control.plan.pending", {
                count: plan.pending.length,
              })}
            </li>
            <li>
              {cancelRunning
                ? t("station.control.plan.cancelRunning", {
                    count: plan.running.length,
                  })
                : t("station.control.plan.keepRunning", {
                    count: plan.running.length,
                  })}
            </li>
            {plan.othersAutopilots > 0 && (
              <li>
                {t("station.control.plan.others", {
                  count: plan.othersAutopilots,
                })}
              </li>
            )}
          </ul>
          <label className="station-control-check">
            <input
              type="checkbox"
              checked={cancelRunning}
              onChange={(event) => setCancelRunning(event.target.checked)}
            />
            {t("station.control.alsoRunning", { count: plan.running.length })}
          </label>
          <p className="run-hint">{t("station.control.pauseAllNote")}</p>
        </div>
        <footer>
          <button ref={keepRef} type="button" onClick={onClose}>
            {t("station.control.keepGoing")}
          </button>
          <button
            type="button"
            className="danger"
            disabled={pausing}
            onClick={() => void confirm()}
          >
            {pausing
              ? t("station.control.pausing")
              : t("station.control.pauseAll")}
          </button>
        </footer>
      </section>
    </div>
  );
}
