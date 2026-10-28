import { useEffect, useId, useMemo, useState } from "react";
import { api, RequestError, type AgentModel, type Run, type Runner } from "../api";

/** The runner capability plan runs need (runnerwire.CapPlan). */
export const CAP_PLAN = "plan";

/** Whether a runner says it can run a breakdown. */
export function canPlan(runner: Runner | undefined): boolean {
  return Boolean(runner?.info?.capabilities?.includes(CAP_PLAN));
}
import { useViewer } from "../components/SignIn";
import { IconSend } from "../icons";
import { useI18n } from "../i18n";
import {
  AgentModelPicker,
  agentCatalog,
  agentModelProblem,
  catalogDefaultModel,
  trimAgentModel,
} from "../runs/AgentModelFields";
import { ownRunners, runnerUnavailableReason } from "../runs/DispatchDialog";
import { openNodeRuns } from "../runs/openRuns";
import { useRuns } from "../runs/runStore";
import { useRunners } from "../runners/runnerStore";

/** The runners a plan run may go to: the viewer's own, online, headless. */
export function eligibleRunners(runners: Runner[], viewer: ReturnType<typeof useViewer>): Runner[] {
  return ownRunners(runners, viewer).filter((runner) => runnerUnavailableReason(runner) === "");
}

/**
 * Breaks the goal down: a plan run on one of the viewer's runners reads the
 * repository and writes task nodes with requires edges under the goal,
 * changing no file. The board shows the tasks as they appear; the person
 * edits them before starting the autopilot.
 */
export function BreakdownForm({ goalNodeId }: { goalNodeId: string }) {
  const { t } = useI18n();
  const viewer = useViewer();
  const runners = useRunners((s) => s.runners);
  const refreshRunners = useRunners((s) => s.refresh);
  const ids = { runner: useId(), repo: useId() };
  useEffect(() => {
    void refreshRunners();
  }, [refreshRunners]);

  const eligible = useMemo(() => eligibleRunners(runners, viewer), [runners, viewer]);
  const [runnerId, setRunnerId] = useState("");
  const runner = eligible.find((item) => item.id === runnerId) ?? eligible[0];
  const catalog = useMemo(() => agentCatalog(runner ? [runner] : []), [runner]);
  const [pick, setPick] = useState<AgentModel | null>(null);
  const agentModel: AgentModel = pick ?? {
    agent: catalog[0]?.kind ?? "",
    model: catalogDefaultModel(catalog[0]),
  };
  const repos = runner?.info?.repos ?? [];
  const [repoChoice, setRepoId] = useState("");
  const repo = repos.find((item) => item.id === repoChoice) ?? repos[0];
  const plans = canPlan(runner);

  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [started, setStarted] = useState<Run | null>(null);
  const problem = agentModelProblem(agentModel);

  const submit = async () => {
    if (!runner || !repo || problem) return;
    setSubmitting(true);
    setError("");
    try {
      const value = trimAgentModel(agentModel);
      const run = await api.createPlanRun({
        nodeId: goalNodeId,
        runnerId: runner.id,
        agent: value.agent,
        model: value.model,
        repoId: repo.id,
      });
      setStarted(run);
      void useRuns.getState().loadRuns(goalNodeId);
    } catch (err) {
      // 409: the runner predates plan runs (or lost the capability).
      if (err instanceof RequestError && err.status === 409) {
        setError(t("common.withHint", { label: t("autopilot.planUnsupported"), hint: err.message }));
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="ap-section" aria-labelledby={`${ids.runner}-title`}>
      <h4 id={`${ids.runner}-title`} className="ap-section-title">
        {t("autopilot.breakdownTitle")}
      </h4>
      <p className="run-hint">{t("autopilot.breakdownHint")}</p>
      {eligible.length === 0 ? (
        <p className="run-hint ap-warning">{t("autopilot.noRunner")}</p>
      ) : (
        <div className="ap-breakdown-fields">
          <label className="run-field" htmlFor={ids.runner}>
            <span>{t("autopilot.runner")}</span>
            <select
              id={ids.runner}
              value={runner?.id ?? ""}
              onChange={(event) => {
                setRunnerId(event.target.value);
                setPick(null);
                setRepoId("");
              }}
            >
              {eligible.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          <div className="run-field">
            <span>{t("autopilot.planner")}</span>
            <AgentModelPicker
              label={t("autopilot.planner")}
              value={agentModel}
              catalog={catalog}
              invalid={problem}
              onChange={setPick}
            />
          </div>
          <label className="run-field" htmlFor={ids.repo}>
            <span>{t("autopilot.repo")}</span>
            <select
              id={ids.repo}
              value={repo?.id ?? ""}
              onChange={(event) => setRepoId(event.target.value)}
            >
              {repos.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name || item.id}
                </option>
              ))}
            </select>
          </label>
          {repos.length === 0 && <p className="run-hint ap-warning">{t("autopilot.noRepo")}</p>}
          {runner && !plans && (
            <p className="run-hint ap-warning">{t("autopilot.planUnsupported")}</p>
          )}
        </div>
      )}
      {error && (
        <p className="autopilot-error" role="alert">
          {error}
        </p>
      )}
      {started && (
        <p className="ap-started" role="status">
          {t("autopilot.breakdownStarted")}{" "}
          <button
            type="button"
            className="run-link-button"
            onClick={() => openNodeRuns(goalNodeId, started.id)}
          >
            {t("autopilot.viewRun")}
          </button>
        </p>
      )}
      <div className="ap-actions">
        <button
          type="button"
          className="run-dispatch-button"
          disabled={submitting || !runner || !repo || !plans || problem !== ""}
          onClick={() => void submit()}
        >
          <IconSend size={13} />{" "}
          {submitting ? t("autopilot.breakingDown") : t("autopilot.breakdown")}
        </button>
      </div>
    </section>
  );
}
