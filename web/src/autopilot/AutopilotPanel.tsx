import { useEffect, useId, useMemo, useState } from "react";
import {
  autopilotEnded,
  type AutopilotPolicy,
  type AutopilotView,
  type TaskInstance,
} from "../api";
import { confirmAction } from "../components/ConfirmDialog";
import { useViewer } from "../components/SignIn";
import { IconSend } from "../icons";
import { formatLocalizedDateTime, useI18n } from "../i18n";
import { agentCatalog } from "../runs/AgentModelFields";
import { ownRunners } from "../runs/DispatchDialog";
import { openNodeRuns } from "../runs/openRuns";
import { useRunners } from "../runners/runnerStore";
import { reportError, useApp } from "../store";
import { AutopilotGrid, AutopilotLegend } from "./AutopilotGrid";
import { useAutopilot } from "./autopilotStore";
import { BreakdownForm, eligibleRunners } from "./BreakdownForm";
import { HumanReviewList } from "./HumanReview";
import { usePlanningRun } from "./planningRun";
import { PolicyEditor } from "./PolicyEditor";
import { defaultPolicy, normalizePolicy, policyProblems, type ImplementChoice } from "./policy";

const KNOWN_STATUS = new Set(["running", "paused", "succeeded", "failed", "cancelled"]);

export function AutopilotStatusChip({ status }: { status: string }) {
  const { t } = useI18n();
  const known = KNOWN_STATUS.has(status) ? status : "unknown";
  return (
    <span className={`ap-run-status ap-run-status-${known}`}>
      {KNOWN_STATUS.has(status) ? t(`autopilot.status.${status}`) : status}
    </span>
  );
}

/** "3 of 8 done · 1 running · 1 waiting · 0 failed". */
function CountsLine({ view }: { view: AutopilotView }) {
  const { t } = useI18n();
  const c = view.run.counts;
  return (
    <p className="ap-counts">
      {t("autopilot.counts", {
        success: c.success,
        total: c.total,
        running: c.running,
        waiting: c.waiting,
        failed: c.failed,
      })}
      {c.skipped > 0 && ` · ${t("autopilot.countSkipped", { count: c.skipped })}`}
    </p>
  );
}

/** The policy to start a run with, and the editor for it. */
function StartForm({
  goalNodeId,
  initial,
  onStarted,
  onCancel,
}: {
  goalNodeId: string;
  initial?: AutopilotPolicy;
  onStarted: () => void;
  onCancel?: () => void;
}) {
  const { t } = useI18n();
  const viewer = useViewer();
  const runners = useRunners((s) => s.runners);
  const refreshRunners = useRunners((s) => s.refresh);
  useEffect(() => {
    void refreshRunners();
  }, [refreshRunners]);
  const mine = useMemo(() => ownRunners(runners, viewer), [runners, viewer]);
  const catalog = useMemo(() => agentCatalog(eligibleRunners(runners, viewer)), [runners, viewer]);
  const [policy, setPolicy] = useState<AutopilotPolicy | null>(initial ?? null);
  const value = policy ?? defaultPolicy(catalog);
  const [choice, setChoice] = useState<ImplementChoice>(
    (initial?.implement ?? []).length > 0 ? "chain" : "advisor",
  );
  const [showErrors, setShowErrors] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const start = useAutopilot((s) => s.start);
  const problems = policyProblems(value, choice);
  const titleId = useId();

  const submit = async () => {
    setShowErrors(true);
    if (problems.length > 0) return;
    setSubmitting(true);
    const ok = await start(goalNodeId, normalizePolicy(value, choice));
    setSubmitting(false);
    if (ok) onStarted();
  };

  return (
    <section className="ap-section" aria-labelledby={titleId}>
      <h4 id={titleId} className="ap-section-title">
        {t("autopilot.startTitle")}
      </h4>
      <p className="run-hint">{t("autopilot.startHint")}</p>
      <PolicyEditor
        value={value}
        onChange={setPolicy}
        choice={choice}
        onChoice={(next) => {
          setChoice(next);
          if (next === "chain" && (value.implement ?? []).length === 0 && catalog[0]) {
            const first = catalog[0];
            const model = (first.models.find((m) => m.default) ?? first.models[0])?.id ?? "";
            setPolicy({ ...value, implement: [{ agent: first.kind, model }] });
          }
        }}
        catalog={catalog}
        runners={mine}
        showErrors={showErrors}
        problems={problems}
      />
      {showErrors && problems.length > 0 && (
        <ul className="autopilot-error ap-problems" role="alert">
          {problems.map((key) => (
            <li key={key}>{t(`autopilot.problem.${key}`)}</li>
          ))}
        </ul>
      )}
      <div className="ap-actions">
        {onCancel && (
          <button type="button" className="ap-btn" onClick={onCancel}>
            {t("common.cancel")}
          </button>
        )}
        <button
          type="button"
          className="run-dispatch-button"
          disabled={submitting}
          onClick={() => void submit()}
        >
          <IconSend size={13} /> {submitting ? t("autopilot.starting") : t("autopilot.start")}
        </button>
      </div>
    </section>
  );
}

/** A run's header, its controls, the human review queue and the grid. */
function RunView({ view, onNewRun }: { view: AutopilotView; onNewRun: () => void }) {
  const { t } = useI18n();
  const run = view.run;
  const tasks = view.tasks ?? [];
  const ended = autopilotEnded(run.status);
  const act = useAutopilot((s) => s.act);
  const taskAct = useAutopilot((s) => s.taskAct);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [acting, setActing] = useState(false);

  const control = async (action: "pause" | "resume" | "cancel") => {
    if (action === "cancel") {
      const ok = await confirmAction({
        title: t("autopilot.cancelTitle"),
        description: t("autopilot.cancelDescription"),
        confirmLabel: t("autopilot.cancel"),
        tone: "danger",
      });
      if (!ok) return;
    }
    setActing(true);
    await act(run.id, action);
    setActing(false);
  };

  const onTaskAction = async (task: TaskInstance, action: "retry" | "skip") => {
    if (action === "skip") {
      const ok = await confirmAction({
        title: t("autopilot.skipTitle", { title: task.title || task.nodeId }),
        description: t("autopilot.skipDescription"),
        confirmLabel: t("autopilot.skip"),
      });
      if (!ok) return;
    }
    setBusy((held) => ({ ...held, [task.nodeId]: true }));
    await taskAct(run.id, task.nodeId, action);
    setBusy((held) => ({ ...held, [task.nodeId]: false }));
  };

  const onOpen = (task: TaskInstance) => {
    if (task.runId) {
      openNodeRuns(task.nodeId, task.runId);
    } else {
      void useApp.getState().openTab(task.nodeId).catch(reportError);
    }
  };

  const review = run.policy.review;
  return (
    <div className="ap-run">
      <header className="ap-run-head">
        <div className="ap-run-title">
          <AutopilotStatusChip status={run.status} />
          <span className="ap-muted">
            {t("autopilot.startedBy", {
              name: run.createdByName || run.createdBy,
              at: formatLocalizedDateTime(run.createdAt),
            })}
          </span>
        </div>
        <div className="ap-run-controls">
          {run.status === "running" && (
            <button
              type="button"
              className="ap-btn"
              disabled={acting}
              onClick={() => void control("pause")}
            >
              {t("autopilot.pause")}
            </button>
          )}
          {run.status === "paused" && (
            <button
              type="button"
              className="ap-btn"
              disabled={acting}
              onClick={() => void control("resume")}
            >
              {t("autopilot.resume")}
            </button>
          )}
          {!ended && (
            <button
              type="button"
              className="ap-btn is-danger"
              disabled={acting}
              onClick={() => void control("cancel")}
            >
              {t("autopilot.cancel")}
            </button>
          )}
          {ended && (
            <button type="button" className="ap-btn" onClick={onNewRun}>
              {t("autopilot.newRun")}
            </button>
          )}
        </div>
      </header>
      <CountsLine view={view} />
      <p className="ap-policy-line">
        {t("autopilot.policyLine", {
          review: t(`autopilot.reviewMode.${review.mode}`),
          parallel: run.policy.maxParallel,
          rounds: run.policy.maxRounds,
        })}
        {run.policy.autoMerge && ` · ${t("autopilot.autoMergeOn")}`}
      </p>
      {run.reason && <p className="run-hint">{run.reason}</p>}
      <HumanReviewList
        autopilotId={run.id}
        tasks={tasks.filter(
          (task) => task.state === "waiting_human" && (!task.hold || task.hold === "sensitive"),
        )}
      />
      <AutopilotLegend />
      <AutopilotGrid
        tasks={tasks}
        policy={run.policy}
        active={!ended}
        busy={busy}
        onOpen={onOpen}
        onTaskAction={(task, action) => void onTaskAction(task, action)}
      />
    </div>
  );
}

/**
 * A breakdown or a revision's replan running on the goal: its tasks are
 * about to change, and after a revision nothing resumes until the person
 * says so (the copilot asks when the replan ends).
 */
export function PlanningNotice({ goalNodeId }: { goalNodeId: string }) {
  const { t } = useI18n();
  const run = usePlanningRun(goalNodeId);
  if (!run) return null;
  return (
    <p className={`ap-planning${run.replan ? " is-revision" : ""}`} role="status">
      <span className="ap-glyph" aria-hidden="true">
        ◌
      </span>{" "}
      {t(run.replan ? "autopilot.revisingNotice" : "autopilot.planningNotice")}{" "}
      <button
        type="button"
        className="run-link-button"
        onClick={() => openNodeRuns(goalNodeId, run.id)}
      >
        {t("autopilot.viewRun")}
      </button>
    </p>
  );
}

/**
 * The Autopilot tab of a node: with no run yet, break the goal down (plan
 * run) and start the autopilot with a policy; with one, its controls, the
 * tasks waiting for a person and the DAG grid.
 */
export function AutopilotPanel({ id }: { id: string }) {
  const { t } = useI18n();
  const autopilotId = useAutopilot((s) => s.byGoal[id]);
  const view = useAutopilot((s) => (autopilotId ? s.views[autopilotId] : undefined));
  const loading = useAutopilot((s) => Boolean(s.loading[id]));
  const error = useAutopilot((s) => s.errors[id] ?? "");
  const loadForGoal = useAutopilot((s) => s.loadForGoal);
  const [newRun, setNewRun] = useState(false);

  useEffect(() => {
    void loadForGoal(id);
  }, [id, loadForGoal]);

  const known = autopilotId !== undefined;
  return (
    <div className="ap-panel">
      <div className="ap-panel-head">
        <h3 className="ap-panel-title">{t("autopilot.title")}</h3>
        <p className="run-hint">{t("autopilot.intro")}</p>
      </div>
      {error && (
        <p className="autopilot-error" role="alert">
          {error}
        </p>
      )}
      <PlanningNotice goalNodeId={id} />
      {!known && loading && <p className="run-hint">{t("autopilot.loading")}</p>}
      {known && (!view || newRun) && (
        <>
          <BreakdownForm goalNodeId={id} />
          <StartForm
            goalNodeId={id}
            initial={view?.run.policy}
            onStarted={() => setNewRun(false)}
            onCancel={view ? () => setNewRun(false) : undefined}
          />
        </>
      )}
      {view && !newRun && <RunView view={view} onNewRun={() => setNewRun(true)} />}
    </div>
  );
}
