// The stages a task's run goes through, in order: "Tester → red → Executor →
// green → CI → Reviewer". Shown where a run is dispatched.

import { useI18n } from "../i18n";
import type { Workflow } from "../types";

export type ChainStep = "tester" | "red" | "executor" | "green" | "judge" | "ci" | "reviewer";

/** Whether the stages run the repo's test command (code tests, not checklists). */
export function stagedNeedsTests(workflow: Workflow | null | undefined): boolean {
  return Boolean(workflow?.stages.tester) && workflow?.judge !== "checklist";
}

/** A task's steps under these stages. Story checklists are judged by a model,
 * with no test command and no CI. */
export function stageChain(workflow: Workflow): ChainStep[] {
  const steps: ChainStep[] = [];
  if (workflow.judge === "checklist") {
    steps.push("executor", "judge");
    if (workflow.stages.reviewer) steps.push("reviewer");
    return steps;
  }
  if (workflow.stages.tester) steps.push("tester", "red");
  steps.push("executor");
  if (workflow.stages.tester) steps.push("green");
  if (workflow.stages.ci) steps.push("ci");
  if (workflow.stages.reviewer) steps.push("reviewer");
  return steps;
}

export function StageChain({ workflow }: { workflow: Workflow }) {
  const { t } = useI18n();
  const steps = stageChain(workflow);
  return (
    <ol className="stage-chain" aria-label={t("stages.chain")}>
      {steps.map((step) => (
        <li key={step} className={`stage-step is-${step}`}>
          {step === "tester" || step === "executor" || step === "reviewer"
            ? t(`roles.name.${step}`)
            : t(`stages.step.${step}`)}
        </li>
      ))}
    </ol>
  );
}
