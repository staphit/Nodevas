// Station's control center, the parts that are not the war room: one button
// that stops everything, the report of what it did, and the loads the war
// room's rows need. Each control is the one the run's or the goal's own view
// offers (same question, same store action); this only puts them in one
// place.

import { useEffect, useId, useMemo, useRef, useState } from "react";

import { confirmAction } from "../components/ConfirmDialog";
import { useViewer } from "../components/SignIn";
import { useAutopilot } from "../autopilot/autopilotStore";
import { IconWarning } from "../icons";
import { useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";
import { useRuns } from "../runs/runStore";
import { useApp } from "../store";
import { canSteer, liveAutopilots, pauseAllPlan, type PauseAllPlan } from "./control";
import { useControl } from "./controlStore";

export function useTitleOf(): (id: string) => string {
  const graph = useApp((s) => s.graph);
  return useMemo(() => {
    const titles = new Map(
      (graph?.nodes ?? []).map((node) => [node.id, node.title]),
    );
    return (id: string) => titles.get(id) || id;
  }, [graph]);
}

/** The active runs and live autopilots, asked for again whenever a project
 * switch (RunsRoot) resets their stores, maybe after Station mounted. */
export function useControlLoads() {
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

/** No runner at all, nothing under way and no agent online: Station has
 * nothing to show but the way to start. Unknown (still loading, or the
 * viewer may not list runners) is not "none". */
export function useNoRunner(busy: boolean): boolean {
  const loaded = useRunners((s) => s.loaded);
  const loading = useRunners((s) => s.loading);
  const error = useRunners((s) => s.error);
  const count = useRunners((s) => s.runners.length);
  useEffect(() => {
    if (!useRunners.getState().loaded) void useRunners.getState().refresh();
  }, []);
  return loaded && !loading && !error && count === 0 && !busy;
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
