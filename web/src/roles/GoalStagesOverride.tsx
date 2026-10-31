// A Goal's workflow-stage override: "Stages: Standard (project) · Override…".
// One big feature can turn QA on without changing the rest of the project.

import { useId, useState } from "react";

import { hasWorkflowOverride } from "../domain/graph/acceptance";
import { costHint, estimateCost, PRESETS, workflowForPreset } from "../domain/graph/stages";
import { useI18n } from "../i18n";
import { openSettings } from "../settings/openSettings";
import { useApp } from "../store";
import type { Workflow, WorkflowPreset } from "../types";
import { presetName } from "./stageLabels";

const OVERRIDE_PRESETS: readonly WorkflowPreset[] = ["light", "standard", "full"];

export function GoalStagesOverride({ id }: { id: string }) {
  const { t } = useI18n();
  const graph = useApp((s) => s.graph);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const groupId = useId();
  const node = graph?.nodes?.find((candidate) => candidate.id === id);
  if (!node || !hasWorkflowOverride(graph, node.kind)) return null;

  const own = node.workflow ?? null;
  const project = graph?.workflow ?? null;
  const shown = own ?? project;
  const summary = shown
    ? t(own ? "stages.goal.own" : "stages.goal.project", { preset: presetName(shown.preset, t) })
    : t("stages.goal.legacy");

  const write = async (workflow: Workflow | null) => {
    setError("");
    const result = await useApp.getState().runGraphCommand({ type: "node.setWorkflow", nodeId: id, workflow });
    if (!result.ok) setError(result.message);
    else setOpen(false);
  };

  const pick = (preset: WorkflowPreset) => {
    // The project's levers and judge carry over; only the stages change.
    const base = workflowForPreset(preset)!;
    void write({ ...(project ?? {}), ...base, judge: project?.judge ?? base.judge });
  };

  return (
    <div className="goal-stages">
      <span className="goal-stages-summary">{summary}</span>
      {own ? (
        <button type="button" className="run-link-button" onClick={() => void write(null)}>
          {t("stages.goal.useProject")}
        </button>
      ) : (
        <button type="button" className="run-link-button" aria-expanded={open} onClick={() => setOpen(!open)}>
          {t("stages.goal.override")}
        </button>
      )}
      {!project && !own && (
        <button type="button" className="run-link-button" onClick={() => openSettings("stages")}>
          {t("stages.goal.settings")}
        </button>
      )}
      {(open || own) && (
        <div className="goal-stages-presets" role="radiogroup" aria-labelledby={groupId}>
          <span id={groupId} className="stages-group-label">
            {t("stages.goal.thisGoal")}
          </span>
          {OVERRIDE_PRESETS.map((preset) => {
            const name = presetName(preset, t);
            const hint = costHint(estimateCost({ preset, stages: PRESETS[preset]! }));
            return (
              <label key={preset} className={`stages-preset${own?.preset === preset ? " is-on" : ""}`}>
                <input
                  type="radio"
                  name={`${groupId}-preset`}
                  value={preset}
                  checked={own?.preset === preset}
                  aria-label={`${name} ${hint}`}
                  onChange={() => pick(preset)}
                />
                <span className="stages-preset-name">{name}</span>
                <span className="stages-preset-cost" data-cost={hint} aria-hidden="true" />
              </label>
            );
          })}
        </div>
      )}
      {error && (
        <p className="acceptance-error" role="status">
          {error}
        </p>
      )}
    </div>
  );
}
