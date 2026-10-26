// Station's control center: every run under way in the project, the
// autopilots driving some of them, and one button that stops them all. Each
// control is the one the run's or the goal's own view offers (same question,
// same store action); this only puts them in one place.

import { useEffect, useId, useMemo, useRef, useState } from "react";

import { runCancellable, runFinal, type AutopilotView } from "../api";
import { AutopilotStatusChip } from "../autopilot/AutopilotPanel";
import { useAutopilot } from "../autopilot/autopilotStore";
import { useAutopilotControl } from "../autopilot/useAutopilotControl";
import { useViewer } from "../components/SignIn";
import { agentWho } from "../game/labels";
import { IconWarning } from "../icons";
import { useI18n } from "../i18n";
import { confirmCancelRun } from "../runs/confirmCancelRun";
import { RunStatusBadge } from "../runs/NodeRuns";
import { openNodeRuns } from "../runs/openRuns";
import { formatCompact, formatCost, totalTokens } from "../runs/runFormat";
import { useRuns } from "../runs/runStore";
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
import { useStation } from "./stationStore";

/** Now, again every `ms` while mounted: the elapsed column counts up. */
function useNow(ms: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(timer);
  }, [ms]);
  return now;
}

function useTitleOf(): (id: string) => string {
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

export function StationControl() {
  const { t } = useI18n();
  const viewer = useViewer();
  const activeProject = useApp((s) => s.activeProject);
  const active = useRuns((s) => s.activeRuns);
  const activeRunsLoaded = useRuns((s) => s.activeRunsLoaded);
  const recent = useStation((s) => s.runs);
  const views = useAutopilot((s) => s.views);
  const autopilotsLoaded = useAutopilot((s) => s.activeLoaded);
  const result = useControl((s) => s.result);
  const error = useControl((s) => s.error);
  const dismiss = useControl((s) => s.dismiss);
  const now = useNow(1000);
  const [showEnded, setShowEnded] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const titleOf = useTitleOf();
  const endedId = useId();

  // A project switch resets the run and autopilot stores (RunsRoot), maybe
  // after this mounted: whenever they come back unloaded, ask again.
  useEffect(() => {
    if (!activeRunsLoaded) void useRuns.getState().loadActiveRuns();
  }, [activeRunsLoaded, activeProject]);
  useEffect(() => {
    if (!autopilotsLoaded) void useAutopilot.getState().loadActive();
  }, [autopilotsLoaded, activeProject]);
  // The last pause-all's report belongs to the project it stopped.
  useEffect(() => () => useControl.getState().dismiss(), [activeProject]);

  const { live, ended } = useMemo(
    () => controlRows(active, recent, views, now),
    [active, recent, views, now],
  );
  const pilots = useMemo(() => liveAutopilots(views), [views]);
  const plan = useMemo(
    () => pauseAllPlan(viewer, active, views),
    [viewer, active, views],
  );
  const anything =
    plan.pause.length + plan.pending.length + plan.running.length > 0;

  return (
    <section
      className="station-panel station-control"
      aria-labelledby="station-control-title"
    >
      <div className="station-panel-head">
        <h2 id="station-control-title">{t("station.control.title")}</h2>
        <span
          className="station-control-count"
          aria-label={t("station.control.activeCount", { count: live.length })}
          title={t("station.control.activeCount", { count: live.length })}
        >
          {live.length}
        </span>
        <button
          type="button"
          className="ap-btn is-danger station-control-stop"
          disabled={!anything}
          onClick={() => setConfirming(true)}
        >
          <IconWarning size={13} /> {t("station.control.pauseAll")}
        </button>
      </div>

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
          <button
            type="button"
            className="run-link-button"
            onClick={dismiss}
            aria-label={t("common.close")}
          >
            ✕
          </button>
        </p>
      )}

      {live.length === 0 && (
        <p className="station-empty">{t("station.control.empty")}</p>
      )}
      {live.length + ended.length > 0 && (
        <table className="station-control-table">
          <thead className={live.length === 0 ? "sr-only" : undefined}>
            <tr>
              <th scope="col">{t("station.control.col.node")}</th>
              <th scope="col">{t("station.control.col.kind")}</th>
              <th scope="col">{t("station.control.col.agent")}</th>
              <th scope="col">{t("station.control.col.runner")}</th>
              <th scope="col">{t("station.control.col.status")}</th>
              <th scope="col">{t("station.control.col.elapsed")}</th>
              <th scope="col">{t("station.control.col.usage")}</th>
              <th scope="col">{t("station.control.col.goal")}</th>
              <th scope="col">
                <span className="sr-only">
                  {t("station.control.col.actions")}
                </span>
              </th>
            </tr>
          </thead>
          {live.length > 0 && (
            <tbody>
              {live.map((row) => (
                <ControlRunRow
                  key={row.run.id}
                  row={row}
                  now={now}
                  titleOf={titleOf}
                />
              ))}
            </tbody>
          )}
          {/* Ended in the last hour: folded away, in the same columns. */}
          {ended.length > 0 && (
            <tbody id={endedId} className="station-control-ended">
              <tr className="station-control-toggle-row">
                <td colSpan={9}>
                  <button
                    type="button"
                    className="run-link-button station-control-toggle"
                    aria-expanded={showEnded}
                    aria-controls={endedId}
                    onClick={() => setShowEnded((open) => !open)}
                  >
                    {showEnded ? "▾" : "▸"}{" "}
                    {t("station.control.ended", { count: ended.length })}
                  </button>
                </td>
              </tr>
              {showEnded &&
                ended.map((row) => (
                  <ControlRunRow
                    key={row.run.id}
                    row={row}
                    now={now}
                    titleOf={titleOf}
                  />
                ))}
            </tbody>
          )}
        </table>
      )}

      <h3 className="station-control-sub">{t("station.control.autopilots")}</h3>
      {pilots.length === 0 ? (
        <p className="station-empty">{t("station.control.noAutopilots")}</p>
      ) : (
        <ul className="station-control-pilots">
          {pilots.map((view) => (
            <AutopilotItem
              key={view.run.id}
              view={view}
              title={titleOf(view.run.goalNodeId)}
            />
          ))}
        </ul>
      )}

      {confirming && (
        <PauseAllDialog plan={plan} onClose={() => setConfirming(false)} />
      )}
    </section>
  );
}

function ControlRunRow({
  row,
  now,
  titleOf,
}: {
  row: ControlRow;
  now: number;
  titleOf: (id: string) => string;
}) {
  const { t } = useI18n();
  const viewer = useViewer();
  const { run, autopilot } = row;
  const cancelling = useRuns((s) => Boolean(s.cancelling[run.id]));
  const cancelError = useRuns((s) => s.cancelErrors[run.id]);
  const stopping =
    useRuns((s) => Boolean(s.stopRequested[run.id])) && !runFinal(run.status);
  const cancelRun = useRuns((s) => s.cancelRun);
  const tokens = totalTokens(run.usage);
  const cost = formatCost(run.usage?.costUsd);
  const steerable = autopilot && canSteer(viewer, autopilot.run.createdBy);

  const onCancel = async () => {
    if (await confirmCancelRun(t, run)) await cancelRun(run.id);
  };

  return (
    <tr className={`station-control-row status-${run.status}`}>
      <td data-label={t("station.control.col.node")}>
        <button
          type="button"
          className="run-link-button station-control-node"
          onClick={() => openNode(run.nodeId)}
        >
          {titleOf(run.nodeId)}
        </button>
      </td>
      <td data-label={t("station.control.col.kind")}>
        {t(`station.control.kind.${controlRunKind(run)}`)}
      </td>
      <td data-label={t("station.control.col.agent")}>
        {agentWho(run.agent, run.model)}
      </td>
      <td data-label={t("station.control.col.runner")}>
        {run.runnerName || "—"}
      </td>
      <td data-label={t("station.control.col.status")}>
        <span>
          <RunStatusBadge status={run.status} />
          {stopping && (
            <span className="station-control-note">
              {t("station.control.stopping")}
            </span>
          )}
        </span>
      </td>
      <td
        data-label={t("station.control.col.elapsed")}
        className="station-control-num"
      >
        {formatElapsed(elapsedMs(run, now))}
      </td>
      <td
        data-label={t("station.control.col.usage")}
        className="station-control-num"
      >
        <span>
          {tokens > 0
            ? t("station.control.tokens", { tokens: formatCompact(tokens) })
            : "—"}
          {cost && <span className="station-control-note">{cost}</span>}
        </span>
      </td>
      <td data-label={t("station.control.col.goal")}>
        {autopilot ? (
          <button
            type="button"
            className="run-link-button"
            onClick={() => openGoal(autopilot.run.goalNodeId)}
          >
            {titleOf(autopilot.run.goalNodeId)}
          </button>
        ) : (
          "—"
        )}
      </td>
      <td className="station-control-actions-cell">
        <div className="station-control-actions">
          <button
            type="button"
            className="ap-btn"
            onClick={() => openNodeRuns(run.nodeId, run.id)}
          >
            {t("station.control.openRun")}
          </button>
          {runCancellable(run.status) && !stopping && (
            <button
              type="button"
              className="ap-btn is-danger"
              disabled={cancelling}
              onClick={() => void onCancel()}
            >
              {cancelling ? t("runs.cancelling") : t("runs.cancel")}
            </button>
          )}
          {autopilot && steerable && <AutopilotSteer view={autopilot} />}
        </div>
        {cancelError && (
          <span className="autopilot-error" role="alert">
            {cancelError}
          </span>
        )}
      </td>
    </tr>
  );
}

/** Pause or resume (adopting a revised goal) an autopilot, and with
 * `withCancel` cancel it: the goal panel's own controls. */
function AutopilotSteer({
  view,
  withCancel = false,
}: {
  view: AutopilotView;
  withCancel?: boolean;
}) {
  const { t } = useI18n();
  const { acting, resumeAction, resumeLabel, control } =
    useAutopilotControl(view);
  const status = view.run.status;
  return (
    <>
      {status === "running" && (
        <button
          type="button"
          className="ap-btn"
          disabled={acting}
          onClick={() => void control("pause")}
        >
          {t("station.control.pauseGoal")}
        </button>
      )}
      {status === "paused" && (
        <button
          type="button"
          className="ap-btn"
          disabled={acting}
          onClick={() => void control(resumeAction)}
        >
          {resumeLabel}
        </button>
      )}
      {withCancel && (status === "running" || status === "paused") && (
        <button
          type="button"
          className="ap-btn is-danger"
          disabled={acting}
          onClick={() => void control("cancel")}
        >
          {t("autopilot.cancel")}
        </button>
      )}
    </>
  );
}

function AutopilotItem({
  view,
  title,
}: {
  view: AutopilotView;
  title: string;
}) {
  const { t } = useI18n();
  const viewer = useViewer();
  const error = useAutopilot((s) => s.errors[view.run.goalNodeId] ?? "");
  const { success, total } = view.run.counts;
  const steerable = canSteer(viewer, view.run.createdBy);
  return (
    <li className="station-control-pilot">
      <div className="station-control-pilot-head">
        <AutopilotStatusChip status={view.run.status} />
        <button
          type="button"
          className="run-link-button station-control-node"
          onClick={() => openGoal(view.run.goalNodeId)}
        >
          {title}
        </button>
      </div>
      <div className="station-control-progress">
        <progress
          max={Math.max(total, 1)}
          value={success}
          aria-label={t("station.goals.progress", { done: success, total })}
        />
        <span>{t("station.goals.progress", { done: success, total })}</span>
      </div>
      <div className="station-control-actions">
        {steerable ? (
          <AutopilotSteer view={view} withCancel />
        ) : (
          <span className="station-control-note">
            {t("station.control.startedBy", {
              name: view.run.createdByName || view.run.createdBy,
            })}
          </span>
        )}
      </div>
      {error && (
        <p className="autopilot-error" role="alert">
          {error}
        </p>
      )}
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
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
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
