/**
 * The analysis drop-down: what the schedule says about the project right now.
 * Every row is a button, because the point of reading a problem here is to
 * jump to the node that has it.
 */

import type { GraphAnalysis } from "../../analysis";

export function GraphAnalysisPanel({
  analysis,
  selectNode,
  nodeTitle,
}: {
  analysis: GraphAnalysis;
  selectNode: (nodeId: string) => void;
  nodeTitle: (id: string | undefined) => string;
}) {
  return (
    <section className="graph-analysis-panel" aria-label="專案分析">
      <div className="analysis-summary">
        <b>關鍵路徑：{analysis.criticalDays} 天</b>
        <span>起點 {analysis.entryNodeIds.size}</span>
        <span>逾期 {analysis.overdue.size}</span>
        <span>依賴違反 {analysis.violations.length}</span>
        <span>受阻 {analysis.blocked.size}</span>
        <span>阻塞源 {analysis.blocking.size}</span>
        {analysis.hasCycle && <span className="danger">偵測到循環依賴</span>}
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
            <b>逾期 · {nodeTitle(id)}</b>
            <small>{messages.join("；")}</small>
          </button>
        ))}
        {analysis.violations.map((item, index) => (
          <button
            type="button"
            key={`${item.kind}-${item.nodeId}-${index}`}
            onClick={() => selectNode(item.nodeId)}
          >
            <b>{item.kind === "actual" ? "實際依賴違反" : "排程衝突"} · {nodeTitle(item.nodeId)}</b>
            <small>{item.message}</small>
          </button>
        ))}
      </div>
    </section>
  );
}
