import { useId } from "react";
import {
  MAX_AUTOPILOT_PARALLEL,
  MAX_FALLBACKS,
  MAX_PIPELINE_ROUNDS,
  MAX_REVIEWERS,
  REVIEW_MODES,
  reviewHasAgents,
  type AgentModel,
  type AutopilotPolicy,
  type Runner,
} from "../api";
import { IconPlus } from "../icons";
import { useI18n } from "../i18n";
import {
  AgentModelPicker,
  agentModelProblem,
  catalogDefaultModel,
  moveItem,
  ReorderButtons,
  type CatalogAgent,
} from "../runs/AgentModelFields";
import type { ImplementChoice } from "./policy";

function range(n: number): number[] {
  return Array.from({ length: n }, (_, i) => i + 1);
}

/** The repositories `runners` offer, deduplicated by id. */
export function reposOf(runners: Runner[]): { id: string; name: string }[] {
  const seen = new Map<string, string>();
  for (const runner of runners) {
    for (const repo of runner.info?.repos ?? []) {
      if (!seen.has(repo.id)) seen.set(repo.id, repo.name || repo.id);
    }
  }
  return [...seen].map(([id, name]) => ({ id, name }));
}

/** An ordered list of agent/model pairs with add, reorder and remove. */
export function AgentModelList({
  value,
  onChange,
  catalog,
  max,
  labelFor,
  addLabel,
  showErrors,
}: {
  value: AgentModel[];
  onChange: (value: AgentModel[]) => void;
  catalog: CatalogAgent[];
  max: number;
  /** "Implementer", "Fallback 1", "Reviewer 2"… for item `index`. */
  labelFor: (index: number) => string;
  addLabel: string;
  showErrors: boolean;
}) {
  const { t } = useI18n();
  return (
    <>
      {value.length > 0 && (
        <ol className="fallback-list autopilot-list">
          {value.map((item, index) => {
            const name = labelFor(index);
            return (
              <li key={index} className="fallback-item">
                <span className="fallback-index" aria-hidden="true">
                  {index + 1}
                </span>
                <AgentModelPicker
                  label={name}
                  value={item}
                  catalog={catalog}
                  invalid={showErrors ? agentModelProblem(item) : ""}
                  onChange={(next) => onChange(value.map((held, i) => (i === index ? next : held)))}
                />
                <ReorderButtons
                  index={index}
                  count={value.length}
                  name={name}
                  onMove={(delta) => onChange(moveItem(value, index, delta))}
                  onRemove={() => onChange(value.filter((_, i) => i !== index))}
                />
              </li>
            );
          })}
        </ol>
      )}
      <div className="fallback-actions">
        <button
          type="button"
          className="run-link-button"
          disabled={value.length >= max}
          onClick={() => {
            const used = new Set(value.map((item) => item.agent));
            const pick = catalog.find((item) => !used.has(item.kind)) ?? catalog[0];
            onChange([...value, { agent: pick?.kind ?? "", model: catalogDefaultModel(pick) }]);
          }}
        >
          <IconPlus size={12} /> {addLabel}
        </button>
        <small className="run-count">
          {t("autopilot.listCount", { count: value.length, max })}
        </small>
      </div>
    </>
  );
}

/**
 * An autopilot policy: who implements (a fixed chain, or the advisor per
 * task), how the PR is reviewed (agents with a quorum, a person, both, or
 * not at all), how many tasks run at once, how many review rounds, whether
 * an approved PR is merged, and where the tasks run.
 */
export function PolicyEditor({
  value,
  onChange,
  choice,
  onChoice,
  catalog,
  runners,
  showErrors,
  problems,
}: {
  value: AutopilotPolicy;
  onChange: (value: AutopilotPolicy) => void;
  choice: ImplementChoice;
  onChoice: (choice: ImplementChoice) => void;
  catalog: CatalogAgent[];
  /** The viewer's own runners, for the pin. */
  runners: Runner[];
  showErrors: boolean;
  problems: string[];
}) {
  const { t } = useI18n();
  const ids = {
    advisor: useId(),
    mode: useId(),
    quorum: useId(),
    parallel: useId(),
    rounds: useId(),
    runner: useId(),
    repo: useId(),
  };
  const reviewers = value.review.reviewers ?? [];
  const agents = reviewHasAgents(value.review.mode);
  const pinned = runners.filter((runner) => !value.runnerId || runner.id === value.runnerId);
  const repos = reposOf(pinned);
  const has = (key: string) => showErrors && problems.includes(key);

  const setReviewers = (next: AgentModel[]) =>
    onChange({
      ...value,
      review: {
        ...value.review,
        reviewers: next,
        quorum: Math.min(value.review.quorum ?? 0, next.length),
      },
    });

  return (
    <div className="autopilot-policy">
      <fieldset className="autopilot-fieldset">
        <legend>{t("autopilot.implement")}</legend>
        <div className="autopilot-choice" role="radiogroup" aria-label={t("autopilot.implement")}>
          <label>
            <input
              type="radio"
              checked={choice === "advisor"}
              onChange={() => onChoice("advisor")}
            />
            {t("autopilot.implementAdvisor")}
          </label>
          <label>
            <input type="radio" checked={choice === "chain"} onChange={() => onChoice("chain")} />
            {t("autopilot.implementChain")}
          </label>
        </div>
        {choice === "advisor" ? (
          <>
            <label className="run-field" htmlFor={ids.advisor}>
              <span>{t("autopilot.advisor")}</span>
              <select
                id={ids.advisor}
                value={value.advisor ?? ""}
                onChange={(event) => onChange({ ...value, advisor: event.target.value })}
              >
                <option value="">{t("autopilot.advisor.rules")}</option>
                <option value="llm">{t("autopilot.advisor.llm")}</option>
              </select>
            </label>
            <small className="run-hint">
              {value.advisor === "llm"
                ? t("autopilot.advisorLlmHint")
                : t("autopilot.advisorRulesHint")}
            </small>
          </>
        ) : (
          <>
            <p className="run-hint">{t("autopilot.chainHint")}</p>
            <AgentModelList
              value={value.implement ?? []}
              onChange={(implement) => onChange({ ...value, implement })}
              catalog={catalog}
              max={MAX_FALLBACKS + 1}
              labelFor={(index) =>
                index === 0 ? t("autopilot.implementer") : t("routing.fallbackN", { n: index })
              }
              addLabel={
                (value.implement ?? []).length === 0
                  ? t("autopilot.addImplementer")
                  : t("routing.addFallback")
              }
              showErrors={showErrors}
            />
            {has("implementEmpty") && (
              <p className="autopilot-error" role="alert">
                {t("autopilot.problem.implementEmpty")}
              </p>
            )}
          </>
        )}
      </fieldset>

      <fieldset className="autopilot-fieldset">
        <legend>{t("autopilot.review")}</legend>
        <label className="run-field" htmlFor={ids.mode}>
          <span>{t("autopilot.reviewMode")}</span>
          <select
            id={ids.mode}
            value={value.review.mode}
            onChange={(event) => {
              const mode = event.target.value;
              let next = reviewers;
              if (reviewHasAgents(mode) && next.length === 0 && catalog[0]) {
                next = [
                  {
                    agent: catalog[0].kind,
                    model: catalogDefaultModel(catalog[0]),
                  },
                ];
              }
              onChange({
                ...value,
                review: { ...value.review, mode, reviewers: next },
              });
            }}
          >
            {REVIEW_MODES.map((mode) => (
              <option key={mode} value={mode}>
                {t(`autopilot.reviewMode.${mode}`)}
              </option>
            ))}
          </select>
        </label>
        <small className="run-hint">{t(`autopilot.reviewModeHint.${value.review.mode}`)}</small>
        {agents && (
          <>
            <AgentModelList
              value={reviewers}
              onChange={setReviewers}
              catalog={catalog}
              max={MAX_REVIEWERS}
              labelFor={(index) => t("autopilot.reviewerN", { n: index + 1 })}
              addLabel={t("autopilot.addReviewer")}
              showErrors={showErrors}
            />
            {has("reviewersEmpty") && (
              <p className="autopilot-error" role="alert">
                {t("autopilot.problem.reviewersEmpty")}
              </p>
            )}
            <label className="run-field" htmlFor={ids.quorum}>
              <span>{t("autopilot.quorum")}</span>
              <select
                id={ids.quorum}
                value={value.review.quorum ?? 0}
                aria-invalid={has("quorum") ? true : undefined}
                onChange={(event) =>
                  onChange({
                    ...value,
                    review: {
                      ...value.review,
                      quorum: Number(event.target.value),
                    },
                  })
                }
              >
                <option value={0}>{t("autopilot.quorumAll", { count: reviewers.length })}</option>
                {range(reviewers.length).map((n) => (
                  <option key={n} value={n}>
                    {t("autopilot.quorumN", { n, count: reviewers.length })}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
      </fieldset>

      <fieldset className="autopilot-fieldset autopilot-grid-fields">
        <legend>{t("autopilot.execution")}</legend>
        <label className="run-field" htmlFor={ids.parallel}>
          <span>{t("autopilot.maxParallel")}</span>
          <select
            id={ids.parallel}
            value={value.maxParallel}
            onChange={(event) => onChange({ ...value, maxParallel: Number(event.target.value) })}
          >
            {range(MAX_AUTOPILOT_PARALLEL).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <label className="run-field" htmlFor={ids.rounds}>
          <span>{t("autopilot.maxRounds")}</span>
          <select
            id={ids.rounds}
            value={value.maxRounds}
            onChange={(event) => onChange({ ...value, maxRounds: Number(event.target.value) })}
          >
            {range(MAX_PIPELINE_ROUNDS).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <label className="run-field" htmlFor={ids.runner}>
          <span>{t("autopilot.runner")}</span>
          <select
            id={ids.runner}
            value={value.runnerId ?? ""}
            onChange={(event) => {
              const runnerId = event.target.value;
              const offered = reposOf(
                runners.filter((runner) => !runnerId || runner.id === runnerId),
              );
              const repoId = offered.some((repo) => repo.id === value.repoId) ? value.repoId : "";
              onChange({ ...value, runnerId, repoId });
            }}
          >
            <option value="">{t("autopilot.anyRunner")}</option>
            {runners.map((runner) => (
              <option key={runner.id} value={runner.id}>
                {runner.name}
                {runner.online ? "" : ` (${t("autopilot.offline")})`}
              </option>
            ))}
          </select>
        </label>
        <label className="run-field" htmlFor={ids.repo}>
          <span>{t("autopilot.repo")}</span>
          <select
            id={ids.repo}
            value={value.repoId ?? ""}
            onChange={(event) => onChange({ ...value, repoId: event.target.value })}
          >
            <option value="">{t("autopilot.anyRepo")}</option>
            {repos.map((repo) => (
              <option key={repo.id} value={repo.id}>
                {repo.name}
              </option>
            ))}
          </select>
        </label>
        <label className="autopilot-check">
          <input
            type="checkbox"
            checked={value.autoMerge}
            onChange={(event) => onChange({ ...value, autoMerge: event.target.checked })}
          />
          <span>
            {t("autopilot.autoMerge")}
            <small className="run-hint">{t("autopilot.autoMergeHint")}</small>
          </span>
        </label>
      </fieldset>
    </div>
  );
}
