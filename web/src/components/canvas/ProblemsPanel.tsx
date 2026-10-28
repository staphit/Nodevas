/**
 * The board's Problems panel [B-06].
 *
 * One place for everything that wants fixing: the explorer's validation
 * warnings and the schedule analysis that used to push the graph down. It
 * opens beside the board rather than above it, so reading the list never
 * moves the cards it is about, and every row takes you to its node.
 */

import { useEffect, useRef } from "react";

import type { GraphAnalysis } from "../../analysis";
import { IconClose, IconWarning } from "../../icons";
import { useI18n } from "../../i18n";
import type { Issue } from "../../types";
import { GraphAnalysisPanel } from "../lane/GraphAnalysisPanel";

/**
 * What the header's Problems button counts: the rows this panel lists as
 * problems. An "info" issue is a hint — a legacy type that behaves as Task —
 * shown at the bottom and never counted.
 */
export function problemCount(analysis: GraphAnalysis, issues: Issue[]): number {
  return (
    analysis.overdue.size +
    analysis.violations.length +
    issues.filter((issue) => issue.severity !== "info").length
  );
}

export interface ProblemsPanelProps {
  analysis: GraphAnalysis;
  issues: Issue[];
  nodeTitle: (id: string | undefined) => string;
  /** Selects the node on the board and opens it. */
  onPick: (nodeId: string) => void;
  onClose: () => void;
}

export function ProblemsPanel({
  analysis,
  issues,
  nodeTitle,
  onPick,
  onClose,
}: ProblemsPanelProps) {
  const { t } = useI18n();
  const count = problemCount(analysis, issues);
  const problems = issues.filter((issue) => issue.severity !== "info");
  const hints = issues.filter((issue) => issue.severity === "info");
  const ref = useRef<HTMLElement>(null);

  // Opened from a button in the header: the keyboard follows it in, so the
  // list is the next thing Tab reaches rather than the far end of the board.
  useEffect(() => {
    ref.current?.focus();
  }, []);

  return (
    <aside
      ref={ref}
      className="problems-panel"
      aria-label={t("board.problems")}
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        event.stopPropagation();
        onClose();
      }}
    >
      <header className="problems-panel-head">
        <h3>
          {t("board.problems")} <span className="problems-count">{count}</span>
        </h3>
        <button
          type="button"
          className="problems-close"
          onClick={onClose}
          aria-label={t("board.problemsClose")}
          title={t("board.problemsClose")}
        >
          <IconClose size={14} />
        </button>
      </header>
      {count === 0 && <p className="problems-empty">{t("board.problemsNone")}</p>}
      {problems.length > 0 && (
        <section className="problems-section" aria-label={t("board.problemsValidation")}>
          <h4>{t("board.problemsValidation")}</h4>
          <ul className="problems-issues">
            {problems.map((issue, index) => {
              const body = (
                <>
                  <IconWarning size={12} />
                  <span>
                    <b>{issue.nodeId ? nodeTitle(issue.nodeId) : t("board.problemsWholeGraph")}</b>
                    {issue.field ? ` · ${issue.field}` : ""}
                    <small>{issue.msg}</small>
                  </span>
                </>
              );
              return (
                <li key={`${issue.nodeId ?? "graph"}-${issue.field ?? ""}-${index}`}>
                  {issue.nodeId ? (
                    <button
                      type="button"
                      className={`problems-issue issue-${issue.severity}`}
                      onClick={() => onPick(issue.nodeId!)}
                    >
                      {body}
                    </button>
                  ) : (
                    <div className={`problems-issue issue-${issue.severity}`}>{body}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}
      <section className="problems-section" aria-label={t("board.problemsSchedule")}>
        <h4>{t("board.problemsSchedule")}</h4>
        <GraphAnalysisPanel analysis={analysis} selectNode={onPick} nodeTitle={nodeTitle} />
      </section>
      {hints.length > 0 && (
        <section className="problems-section problems-hints" aria-label={t("board.problemsHints")}>
          <h4>{t("board.problemsHints")}</h4>
          <ul className="problems-issues">
            {hints.map((issue, index) => (
              <li key={`hint-${issue.nodeId ?? "graph"}-${issue.field ?? ""}-${index}`}>
                {issue.nodeId ? (
                  <button
                    type="button"
                    className="problems-issue issue-info"
                    onClick={() => onPick(issue.nodeId!)}
                  >
                    <small>{issue.msg}</small>
                  </button>
                ) : (
                  <div className="problems-issue issue-info">
                    <small>{issue.msg}</small>
                  </div>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </aside>
  );
}
