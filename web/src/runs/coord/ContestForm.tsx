import { useState } from "react";
import {
  CAP_COORD,
  MAX_CONTEST_ENTRIES,
  type ContestEntrySpec,
  type Runner,
} from "../../api";
import { agentName } from "../../game/labels";
import { useI18n } from "../../i18n";
import { runnerDisplayName, runnerModelLabel } from "../../runners/runnerName";
import { agentCatalog, catalogDefaultModel } from "../AgentModelFields";

export interface ContestDraft {
  entries: ContestEntrySpec[];
  reviewer: ContestEntrySpec | null;
}

export interface ContestFormProps {
  runners: readonly Runner[];
  value?: ContestDraft;
  shapeError?: boolean;
  disabled?: boolean;
  onChange?: (draft: ContestDraft) => void;
  onSubmit?: (draft: ContestDraft) => void;
  onCancel?: () => void;
}

const ENTRY_LABELS = ["A", "B", "C"] as const;
const MIN_CONTEST_ENTRIES = 2;

function availableOptions(runners: readonly Runner[]): ContestEntrySpec[] {
  const options: ContestEntrySpec[] = [];
  const defaultRunnerId = runners[0]?.id ?? "";
  const catalog = agentCatalog([...runners]);

  for (const agent of catalog) {
    const defModel = catalogDefaultModel(agent);
    if (defModel) {
      options.push({ runnerId: defaultRunnerId, agent: agent.kind, model: defModel });
    }
  }
  for (const agent of catalog) {
    for (const model of agent.models) {
      if (!options.some((o) => o.agent === agent.kind && o.model === model.id)) {
        options.push({ runnerId: defaultRunnerId, agent: agent.kind, model: model.id });
      }
    }
  }
  if (options.length === 0) {
    options.push(
      { runnerId: defaultRunnerId, agent: "claude", model: "default" },
      { runnerId: defaultRunnerId, agent: "codex", model: "default" },
    );
  } else if (options.length === 1) {
    options.push({ ...options[0] });
  }
  return options;
}

function buildInitialDraft(runners: readonly Runner[]): ContestDraft {
  const opts = availableOptions(runners);
  return {
    entries: [opts[0], opts[1]],
    reviewer: null,
  };
}

function pickUnusedOption(
  runners: readonly Runner[],
  existing: readonly ContestEntrySpec[],
): ContestEntrySpec {
  const opts = availableOptions(runners);
  const unused = opts.find(
    (opt) => !existing.some((e) => e.agent === opt.agent && e.model === opt.model),
  );
  return unused ? { ...unused } : { ...opts[0] };
}

function hasDuplicateEntries(entries: readonly ContestEntrySpec[]): boolean {
  const seen = new Set<string>();
  for (const entry of entries) {
    const key = `${entry.agent.trim().toLowerCase()}::${entry.model.trim().toLowerCase()}`;
    if (seen.has(key)) return true;
    seen.add(key);
  }
  return false;
}

function runnerSupportsCoord(runner: Runner | undefined): boolean {
  if (!runner) return false;
  return Boolean(runner.info?.capabilities?.includes(CAP_COORD));
}

export function ContestForm({
  runners,
  value,
  shapeError = false,
  disabled = false,
  onChange,
  onSubmit,
  onCancel,
}: ContestFormProps) {
  const { t } = useI18n();
  const [internalDraft, setInternalDraft] = useState<ContestDraft>(() =>
    value ?? buildInitialDraft(runners),
  );

  const draft = value ?? internalDraft;
  const catalog = agentCatalog([...runners]);
  const defaultRunnerId = runners[0]?.id ?? "";

  const updateDraft = (next: ContestDraft) => {
    setInternalDraft(next);
    onChange?.(next);
  };

  const updateEntry = (index: number, patch: Partial<ContestEntrySpec>) => {
    const nextEntries = draft.entries.map((entry, idx) =>
      idx === index ? { ...entry, ...patch } : entry,
    );
    updateDraft({ ...draft, entries: nextEntries });
  };

  const addEntry = () => {
    if (draft.entries.length >= MAX_CONTEST_ENTRIES) return;
    const next = pickUnusedOption(runners, draft.entries);
    updateDraft({ ...draft, entries: [...draft.entries, next] });
  };

  const removeEntry = (index: number) => {
    if (draft.entries.length <= MIN_CONTEST_ENTRIES) return;
    updateDraft({
      ...draft,
      entries: draft.entries.filter((_, idx) => idx !== index),
    });
  };

  const toggleReviewer = (enabled: boolean) => {
    if (!enabled) {
      updateDraft({ ...draft, reviewer: null });
      return;
    }
    const opts = availableOptions(runners);
    updateDraft({ ...draft, reviewer: draft.reviewer ?? { ...opts[0] } });
  };

  const updateReviewer = (patch: Partial<ContestEntrySpec>) => {
    if (!draft.reviewer) return;
    updateDraft({ ...draft, reviewer: { ...draft.reviewer, ...patch } });
  };

  const anyOutdated =
    runners.length > 0 &&
    (draft.entries.some((entry) => {
      const r = runners.find((item) => item.id === (entry.runnerId || defaultRunnerId)) ?? runners[0];
      return !runnerSupportsCoord(r);
    }) ||
      (draft.reviewer !== null &&
        !runnerSupportsCoord(
          runners.find((item) => item.id === (draft.reviewer?.runnerId || defaultRunnerId)) ??
            runners[0],
        )));

  const duplicate = hasDuplicateEntries(draft.entries);
  const totalRuns = draft.entries.length + (draft.reviewer ? 1 : 0);
  const canSubmit =
    !disabled &&
    !shapeError &&
    !anyOutdated &&
    !duplicate &&
    draft.entries.length >= MIN_CONTEST_ENTRIES &&
    draft.entries.length <= MAX_CONTEST_ENTRIES;

  return (
    <div className="coord-contest-form">
      <div className="coord-contest-head">
        <span className="coord-contest-title">
          {t("contest.title", { count: draft.entries.length })}
        </span>
        <span className="coord-contest-budget">{t("contest.budget", { count: totalRuns })}</span>
      </div>

      {shapeError && (
        <p className="coord-contest-error" role="alert">
          {t("contest.err.shape")}
        </p>
      )}
      {anyOutdated && (
        <p className="coord-contest-error" role="alert">
          {t("contest.err.outdated")}
        </p>
      )}
      {duplicate && !anyOutdated && !shapeError && (
        <p className="coord-contest-error" role="alert">
          {t("contest.title", { count: draft.entries.length })}
        </p>
      )}

      <div className="coord-contest-entries">
        {draft.entries.map((entry, index) => {
          const label = ENTRY_LABELS[index] ?? String(index + 1);
          const entryTitle = t("contest.entry", { label });
          const currentAgent = catalog.find((a) => a.kind === entry.agent) ?? catalog[0];
          const models = currentAgent?.models ?? [];

          return (
            <div
              key={label}
              data-testid={`contest-entry-${index}`}
              className="coord-contest-entry"
            >
              <div className="coord-contest-entry-bar">
                <span className="coord-contest-entry-label">{entryTitle}</span>
                {draft.entries.length > MIN_CONTEST_ENTRIES && (
                  <button
                    type="button"
                    className="coord-btn is-small"
                    disabled={disabled}
                    onClick={() => removeEntry(index)}
                  >
                    {t("contest.removeEntry")}
                  </button>
                )}
              </div>

              {runners.length > 1 && (
                <div
                  className="coord-radio-row"
                  role="radiogroup"
                  aria-label={`${entryTitle} ${t("runs.runner")}`}
                >
                  {runners.map((runner) => {
                    const checked = (entry.runnerId || defaultRunnerId) === runner.id;
                    return (
                      <button
                        key={runner.id}
                        type="button"
                        role="radio"
                        aria-checked={checked}
                        disabled={disabled}
                        className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                        onClick={() => updateEntry(index, { runnerId: runner.id })}
                      >
                        {runnerDisplayName(t, runner.name)}
                      </button>
                    );
                  })}
                </div>
              )}

              <div
                className="coord-radio-row"
                role="radiogroup"
                aria-label={`${entryTitle} ${t("runs.agent")}`}
              >
                {catalog.map((agent) => {
                  const checked = entry.agent === agent.kind;
                  return (
                    <button
                      key={agent.kind}
                      type="button"
                      role="radio"
                      aria-checked={checked}
                      disabled={disabled}
                      className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                      onClick={() =>
                        updateEntry(index, {
                          agent: agent.kind,
                          model: catalogDefaultModel(agent),
                        })
                      }
                    >
                      {agentName(agent.kind)}
                    </button>
                  );
                })}
              </div>

              {models.length > 0 && (
                <div
                  className="coord-radio-row"
                  role="radiogroup"
                  aria-label={`${entryTitle} ${t("runs.model")}`}
                >
                  {models.map((model) => {
                    const checked = entry.model === model.id;
                    return (
                      <button
                        key={model.id}
                        type="button"
                        role="radio"
                        aria-checked={checked}
                        disabled={disabled}
                        className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                        onClick={() => updateEntry(index, { model: model.id })}
                      >
                        {runnerModelLabel(t, model)}
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {draft.entries.length < MAX_CONTEST_ENTRIES && (
        <button
          type="button"
          className="coord-btn coord-add-entry"
          disabled={disabled}
          onClick={addEntry}
        >
          {t("contest.addEntry")}
        </button>
      )}

      <div className="coord-contest-reviewer">
        <div
          className="coord-radio-row"
          role="radiogroup"
          aria-label={t("contest.reviewer")}
        >
          <button
            type="button"
            role="radio"
            aria-checked={draft.reviewer === null}
            disabled={disabled}
            className={`coord-radio-chip${draft.reviewer === null ? " is-selected" : ""}`}
            onClick={() => toggleReviewer(false)}
          >
            {t("contest.reviewerNone")}
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={draft.reviewer !== null}
            disabled={disabled}
            className={`coord-radio-chip${draft.reviewer !== null ? " is-selected" : ""}`}
            onClick={() => toggleReviewer(true)}
          >
            {t("contest.reviewer")}
          </button>
        </div>

        {draft.reviewer && (() => {
          const reviewerAgent = catalog.find((a) => a.kind === draft.reviewer?.agent) ?? catalog[0];
          const reviewerModels = reviewerAgent?.models ?? [];
          return (
            <div className="coord-reviewer-pickers" data-testid="contest-reviewer-pickers">
              {runners.length > 1 && (
                <div
                  className="coord-radio-row"
                  role="radiogroup"
                  aria-label={`${t("contest.reviewer")} ${t("runs.runner")}`}
                >
                  {runners.map((runner) => {
                    const checked = (draft.reviewer?.runnerId || defaultRunnerId) === runner.id;
                    return (
                      <button
                        key={runner.id}
                        type="button"
                        role="radio"
                        aria-checked={checked}
                        disabled={disabled}
                        className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                        onClick={() => updateReviewer({ runnerId: runner.id })}
                      >
                        {runnerDisplayName(t, runner.name)}
                      </button>
                    );
                  })}
                </div>
              )}
              <div
                className="coord-radio-row"
                role="radiogroup"
                aria-label={`${t("contest.reviewer")} ${t("runs.agent")}`}
              >
                {catalog.map((agent) => {
                  const checked = draft.reviewer?.agent === agent.kind;
                  return (
                    <button
                      key={agent.kind}
                      type="button"
                      role="radio"
                      aria-checked={checked}
                      disabled={disabled}
                      className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                      onClick={() =>
                        updateReviewer({
                          agent: agent.kind,
                          model: catalogDefaultModel(agent),
                        })
                      }
                    >
                      {agentName(agent.kind)}
                    </button>
                  );
                })}
              </div>
              {reviewerModels.length > 0 && (
                <div
                  className="coord-radio-row"
                  role="radiogroup"
                  aria-label={`${t("contest.reviewer")} ${t("runs.model")}`}
                >
                  {reviewerModels.map((model) => {
                    const checked = draft.reviewer?.model === model.id;
                    return (
                      <button
                        key={model.id}
                        type="button"
                        role="radio"
                        aria-checked={checked}
                        disabled={disabled}
                        className={`coord-radio-chip${checked ? " is-selected" : ""}`}
                        onClick={() => updateReviewer({ model: model.id })}
                      >
                        {runnerModelLabel(t, model)}
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })()}
      </div>

      {(onSubmit || onCancel) && (
        <div className="coord-contest-actions">
          {onCancel && (
            <button type="button" className="coord-btn" onClick={onCancel}>
              {t("common.cancel")}
            </button>
          )}
          {onSubmit && (
            <button
              type="button"
              className="coord-btn is-primary"
              disabled={!canSubmit}
              onClick={() => canSubmit && onSubmit(draft)}
            >
              {t("common.confirm")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
