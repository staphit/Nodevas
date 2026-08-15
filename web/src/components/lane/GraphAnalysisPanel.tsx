/**
 * The analysis drop-down: what the schedule says about the project right now.
 * Every row is a button, because the point of reading a problem here is to
 * jump to the node that has it.
 */

import type { GraphAnalysis } from "../../analysis";
import { useI18n } from "../../i18n";

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
        <b>{t("analysis.criticalPath", { days: String(analysis.criticalDays) })}</b>
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
            <small>{messages.join("；")}</small>
          </button>
        ))}
        {analysis.violations.map((item, index) => (
          <button
            type="button"
            key={`${item.kind}-${item.nodeId}-${index}`}
            onClick={() => selectNode(item.nodeId)}
          >
            <b>{t(item.kind === "actual" ? "analysis.actualViolation" : "analysis.scheduleConflict", { title: nodeTitle(item.nodeId) })}</b>
            <small>{item.message}</small>
          </button>
        ))}
      </div>
    </section>
  );
}
