/**
 * The analysis drop-down: what the schedule says about the project right now.
 * Every row is a button, because the point of reading a problem here is to
 * jump to the node that has it.
 */

import type { DependencyViolation, GraphAnalysis, OverdueReason } from "../../analysis";
import { useI18n } from "../../i18n";

type Translate = ReturnType<typeof useI18n>["t"];

function overdueText(t: Translate, reason: OverdueReason): string {
  return t(`analysis.overdue.${reason.kind}`, { date: reason.date });
}

function violationText(
  t: Translate,
  item: DependencyViolation,
  nodeTitle: (id: string | undefined) => string,
): string {
  const { reason } = item;
  const titles = item.sourceIds.map(nodeTitle).join(t("analysis.titleSeparator"));
  switch (reason.kind) {
    case "gate-incomplete":
      return t("analysis.violation.gateIncomplete", { operator: reason.operator.toUpperCase() });
    case "gate-false":
      return t("analysis.violation.gateFalse", { operator: reason.operator.toUpperCase(), titles });
    case "requires-invalid":
      return t("analysis.violation.requiresInvalid");
    case "requires-false":
      return t("analysis.violation.requiresFalse");
    case "requires-pending":
      return t("analysis.violation.requiresPending", { titles });
    case "schedule":
      return t("analysis.violation.schedule", { date: reason.date });
  }
}

export function GraphAnalysisPanel({
  analysis,
  selectNode,
  nodeTitle,
}: {
  analysis: GraphAnalysis;
  selectNode: (nodeId: string) => void;
  nodeTitle: (id: string | undefined) => string;
}) {
  const { t } = useI18n();
  return (
    <section className="graph-analysis-panel" aria-label={t("analysis.aria")}>
      <div className="analysis-summary">
        <b>
          {analysis.criticalDays === 1
            ? t("analysis.criticalPathOne")
            : t("analysis.criticalPath", { days: String(analysis.criticalDays) })}
        </b>
        <span>{t("analysis.entry", { count: String(analysis.entryNodeIds.size) })}</span>
        <span>{t("analysis.overdue", { count: String(analysis.overdue.size) })}</span>
        <span>{t("analysis.violations", { count: String(analysis.violations.length) })}</span>
        <span>{t("analysis.blocked", { count: String(analysis.blocked.size) })}</span>
        <span>{t("analysis.blocking", { count: String(analysis.blocking.size) })}</span>
        {analysis.hasCycle && <span className="danger">{t("analysis.cycle")}</span>}
      </div>
      {!!analysis.criticalPath.length && (
        <div className="analysis-path">
          {analysis.criticalPath.map((id, index) => (
            <span key={id}>
              {index > 0 && " → "}
              <button type="button" onClick={() => selectNode(id)}>
                {nodeTitle(id)}
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="analysis-items">
        {[...analysis.overdue].map(([id, messages]) => (
          <button type="button" key={`overdue-${id}`} onClick={() => selectNode(id)}>
            <b>{t("analysis.overdueItem", { title: nodeTitle(id) })}</b>
            <small>{messages.map((reason) => overdueText(t, reason)).join(t("analysis.separator"))}</small>
          </button>
        ))}
        {analysis.violations.map((item, index) => (
          <button
            type="button"
            key={`${item.kind}-${item.nodeId}-${index}`}
            onClick={() => selectNode(item.nodeId)}
          >
            <b>{t(item.kind === "actual" ? "analysis.actualViolation" : "analysis.scheduleConflict", { title: nodeTitle(item.nodeId) })}</b>
            <small>{violationText(t, item, nodeTitle)}</small>
          </button>
        ))}
      </div>
    </section>
  );
}
