// Settings: a project's workflow stages, presets with cost hints, custom
// toggles and the cost levers. Mounted under Agents for now (row "stages");
// it moves to "This project" once that section is free to change.
//
// Stored in the project (graph.yaml workflow), so it syncs with the board.
// The hub refuses an agent's change, and enforces the active set: an agent
// cannot skip an enabled stage.

import { useEffect, useId, useState } from "react";

import {
  costHint,
  DEFAULT_TEST_GLOBS,
  estimateCost,
  PRESET_ORDER,
  PRESETS,
  presetOf,
  STAGE_KEYS,
  WORKFLOW_BOUNDS,
  WORKFLOW_DEFAULTS,
  workflowForPreset,
} from "../domain/graph/stages";
import { useI18n } from "../i18n";
import { useApp } from "../store";
import type { Workflow, WorkflowPreset, WorkflowStages } from "../types";
import { exactCost, presetName } from "./stageLabels";

type Translate = (key: string, values?: Record<string, string | number>) => string;

/** The label of one stage toggle; a story project's tests are checklists. */
export function stageLabel(key: keyof WorkflowStages, story: boolean, t: Translate): string {
  if (key === "tester" && story) return t("stages.stage.checklist");
  return t(`stages.stage.${key}`);
}

/** A preset's stages, keeping the levers (and the judge) already chosen. */
function withPreset(current: Workflow | null, preset: WorkflowPreset, story: boolean): Workflow {
  const base = workflowForPreset(preset)!;
  const judge = current?.judge ?? (story ? "checklist" : "");
  if (preset === "custom") return { ...current, ...base, stages: { ...(current?.stages ?? PRESETS.standard!) }, preset, judge };
  return { ...current, ...base, judge };
}

/** Writes the project's workflow; returns the refusal, or "". */
async function saveWorkflow(workflow: Workflow | null): Promise<string> {
  const result = await useApp.getState().runGraphCommand({ type: "workflow.setStages", workflow });
  return result.ok ? "" : result.message || "";
}

function Lever({
  label,
  value,
  fallback,
  bounds,
  hint,
  onCommit,
}: {
  label: string;
  value: number | undefined;
  fallback: number;
  bounds: readonly [number, number];
  hint?: string;
  onCommit: (value: number) => void;
}) {
  const id = useId();
  const shown = value ?? fallback;
  const [draft, setDraft] = useState(String(shown));
  useEffect(() => setDraft(String(shown)), [shown]);
  const commit = () => {
    const next = Number(draft);
    if (!Number.isInteger(next) || next < bounds[0] || next > bounds[1]) {
      setDraft(String(shown));
      return;
    }
    if (next !== shown) onCommit(next);
  };
  return (
    <div className="run-field stages-lever">
      <label className="run-field-label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        type="number"
        inputMode="numeric"
        min={bounds[0]}
        max={bounds[1]}
        step={1}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            commit();
          }
        }}
      />
      {hint && <small className="run-hint">{hint}</small>}
    </div>
  );
}

export function WorkflowStagesSettings() {
  const { t } = useI18n();
  const workflow = useApp((s) => s.graph?.workflow ?? null);
  const story = useApp((s) => s.graph?.type === "story");
  const [error, setError] = useState("");
  const [globs, setGlobs] = useState("");
  const groupLabelId = useId();
  const globsId = useId();

  // The pick shows straight away; the project's answer replaces it (or a
  // refusal drops it).
  const [pending, setPending] = useState<WorkflowPreset | null>(null);
  useEffect(() => setPending(null), [workflow]);
  const current = pending ?? workflow?.preset ?? null;
  const shownGlobs = (workflow?.testGlobs?.length ? workflow.testGlobs : DEFAULT_TEST_GLOBS).join("\n");
  useEffect(() => setGlobs(shownGlobs), [shownGlobs]);

  const write = async (next: Workflow | null) => {
    setError("");
    setError(await saveWorkflow(next));
  };

  const pick = (preset: WorkflowPreset) => {
    setPending(preset);
    void saveWorkflow(withPreset(workflow, preset, story)).then((refusal) => {
      setError(refusal);
      if (refusal) setPending(null);
    });
  };

  const toggle = (key: keyof WorkflowStages) => {
    if (!workflow) return;
    const stages = { ...workflow.stages, [key]: !workflow.stages[key] };
    void write({ ...workflow, stages, preset: presetOf(stages) });
  };

  const lever = (key: "reviewerSkipBelowLines" | "maxQaRounds" | "maxTesterRounds" | "maxGreenTries") => (value: number) => {
    if (!workflow) return;
    void write({ ...workflow, [key]: value });
  };

  const commitGlobs = () => {
    if (!workflow) return;
    const list = globs
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .slice(0, WORKFLOW_BOUNDS.testGlobs);
    const same = list.join("\n") === DEFAULT_TEST_GLOBS.join("\n");
    const next: Workflow = { ...workflow };
    if (same || list.length === 0) delete next.testGlobs;
    else next.testGlobs = list;
    if (JSON.stringify(next.testGlobs ?? null) === JSON.stringify(workflow.testGlobs ?? null)) return;
    void write(next);
  };

  const stages = workflow?.stages;
  return (
    <div className="stages-settings">
      <p className="runner-hint">{t("stages.description")}</p>
      {!workflow && <p className="stages-legacy">{t("stages.legacyBanner")}</p>}

      <span id={groupLabelId} className="stages-group-label">
        {t("stages.title")}
      </span>
      <div className="stages-presets" role="radiogroup" aria-labelledby={groupLabelId}>
        {PRESET_ORDER.map((preset) => {
          const name = presetName(preset, t);
          const presetStages = PRESETS[preset];
          const hint = presetStages
            ? costHint(estimateCost({ preset, stages: presetStages, judge: story ? "checklist" : "" }))
            : "";
          return (
            <label key={preset} className={`stages-preset${current === preset ? " is-on" : ""}`}>
              <input
                type="radio"
                name={`${groupLabelId}-preset`}
                value={preset}
                checked={current === preset}
                aria-label={hint ? `${name} ${hint}` : name}
                onChange={() => pick(preset)}
              />
              <span className="stages-preset-name">{name}</span>
              {/* Drawn from the attribute, so the cost reads once in the
                  radio's name and the custom line below stays the only text. */}
              {hint && <span className="stages-preset-cost" data-cost={hint} aria-hidden="true" />}
              <small className="stages-preset-what">{t(`stages.presetWhat.${preset}`)}</small>
            </label>
          );
        })}
      </div>

      {workflow && stages && (
        <>
          <fieldset className="stages-toggles">
            <legend>{t("stages.stagesLegend")}</legend>
            {STAGE_KEYS.map((key) => (
              <label key={key} className="route-toggle">
                <input type="checkbox" checked={Boolean(stages[key])} onChange={() => toggle(key)} />
                <span>{stageLabel(key, story, t)}</span>
              </label>
            ))}
          </fieldset>
          <p className="stages-cost">{t("stages.costLine", { cost: exactCost(workflow) })}</p>

          <div className="stages-levers">
            {stages.reviewer && (
              <Lever
                label={t("stages.lever.reviewerSkip")}
                value={workflow.reviewerSkipBelowLines}
                fallback={0}
                bounds={WORKFLOW_BOUNDS.reviewerSkipBelowLines}
                hint={t("stages.lever.reviewerSkipHint")}
                onCommit={lever("reviewerSkipBelowLines")}
              />
            )}
            {stages.goalQa && (
              <Lever
                label={t("stages.lever.maxQaRounds")}
                value={workflow.maxQaRounds}
                fallback={WORKFLOW_DEFAULTS.maxQaRounds}
                bounds={WORKFLOW_BOUNDS.maxQaRounds}
                hint={t("stages.lever.maxQaRoundsHint")}
                onCommit={lever("maxQaRounds")}
              />
            )}
            {stages.tester && (
              <>
                <Lever
                  label={t("stages.lever.maxTesterRounds")}
                  value={workflow.maxTesterRounds}
                  fallback={WORKFLOW_DEFAULTS.maxTesterRounds}
                  bounds={WORKFLOW_BOUNDS.maxTesterRounds}
                  onCommit={lever("maxTesterRounds")}
                />
                <Lever
                  label={t("stages.lever.maxGreenTries")}
                  value={workflow.maxGreenTries}
                  fallback={WORKFLOW_DEFAULTS.maxGreenTries}
                  bounds={WORKFLOW_BOUNDS.maxGreenTries}
                  onCommit={lever("maxGreenTries")}
                />
              </>
            )}
          </div>
          <p className="run-hint">{t("stages.roleBudgets")}</p>

          {stages.tester && !story && (
            <details className="route-sub">
              <summary>{t("stages.testGlobs")}</summary>
              <label className="run-field" htmlFor={globsId}>
                <span>{t("stages.testGlobsHint")}</span>
              </label>
              <textarea
                id={globsId}
                className="stages-globs"
                rows={6}
                spellCheck={false}
                value={globs}
                onChange={(event) => setGlobs(event.target.value)}
                onBlur={commitGlobs}
              />
            </details>
          )}

          <button type="button" className="run-link-button" onClick={() => void write(null)}>
            {t("stages.backToLegacy")}
          </button>
        </>
      )}

      {error && (
        <div className="notify-message error" role="alert">
          {error}
        </div>
      )}
    </div>
  );
}
