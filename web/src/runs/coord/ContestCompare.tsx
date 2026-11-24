import { useEffect, useState } from "react";
import type {
  Contest,
  ContestApply,
  ContestCompare as ContestCompareData,
  ContestCompareEntry,
  PickActionResult,
  PickContestRequest,
  Run,
} from "../../api";
import { agentColorKey, agentName } from "../../game/labels";
import { CastPortrait } from "../../game/warroom/castArt";
import { formatList, useI18n } from "../../i18n";
import { WIDE_QUERY } from "../DiffViewer";
import { LineCounts } from "../RunChanges";
import { workspaceCodeText } from "../workspaceStore";

export interface ContestCompareProps {
  contest: Contest;
  compare: ContestCompareData;
  runs?: readonly Run[] | null;
  pickActions?: readonly PickActionResult[] | null;
  simple?: boolean;
  disabled?: boolean;
  onPick?: (req: PickContestRequest) => void | Promise<void>;
  onShowChanges?: (runId: string) => void;
}

function useWideLayout(): boolean {
  const [wide, setWide] = useState(() => {
    if (typeof window === "undefined" || !window.matchMedia) return true;
    return window.matchMedia(WIDE_QUERY).matches;
  });
  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mql = window.matchMedia(WIDE_QUERY);
    const onChange = (event: MediaQueryListEvent) => setWide(event.matches);
    setWide(mql.matches);
    mql.addEventListener?.("change", onChange);
    return () => mql.removeEventListener?.("change", onChange);
  }, []);
  return wide;
}

export function ContestCompare({
  contest,
  compare,
  runs,
  pickActions,
  simple = false,
  disabled = false,
  onPick,
  onShowChanges,
}: ContestCompareProps) {
  const { t } = useI18n();
  const wide = useWideLayout();
  const entries = compare.entries ?? [];
  const [activeTab, setActiveTab] = useState(0);
  const [pickingRunId, setPickingRunId] = useState<string | null>(null);
  const [discardOthers, setDiscardOthers] = useState(true);
  const [confirmingApply, setConfirmingApply] = useState<ContestApply | null>(null);

  // Anonymity rule (§C.4.2, §C.8): show labels (A/B/C) and NOT agent/model names before scores.
  const revealIdentity = Boolean(compare.scores) || contest.state === "decided";

  const scoreFor = (label: string) =>
    compare.scores?.scores?.find((item) => item.entry === label) ?? null;

  const refusedDiscards = (pickActions ?? []).filter(
    (act) =>
      act.action === "discard" &&
      (act.status === "failed" || Boolean(act.code)),
  );

  const canPickEntry = (entry: ContestCompareEntry): boolean =>
    Boolean(onPick) &&
    !disabled &&
    (contest.state === "ready" || contest.state === "judging") &&
    entry.status === "succeeded";

  const handleSelectApply = (entry: ContestCompareEntry, apply: ContestApply) => {
    const otherLabels = entries
      .filter((item) => item.runId !== entry.runId)
      .map((item) => item.label);
    if (discardOthers && otherLabels.length > 0) {
      setConfirmingApply(apply);
      return;
    }
    void onPick?.({ runId: entry.runId, apply, discardOthers: false });
    setPickingRunId(null);
    setConfirmingApply(null);
  };

  const handleConfirmPick = (entry: ContestCompareEntry) => {
    if (!confirmingApply) return;
    void onPick?.({
      runId: entry.runId,
      apply: confirmingApply,
      discardOthers: true,
    });
    setPickingRunId(null);
    setConfirmingApply(null);
  };

  const renderEntryCard = (entry: ContestCompareEntry) => {
    const entryTitle = t("contest.entry", { label: entry.label });
    const entryScore = scoreFor(entry.label);
    const files = entry.files ?? [];
    const hasChanges = files.length > 0 || entry.additions > 0 || entry.deletions > 0;
    const isWinner = contest.winnerRunId === entry.runId;
    const isPicking = pickingRunId === entry.runId;
    const otherLabels = entries
      .filter((item) => item.runId !== entry.runId)
      .map((item) => item.label);

    return (
      <div
        key={entry.runId || entry.label}
        className={`coord-compare-col${isWinner ? " is-winner" : ""}`}
        data-testid={`contest-col-${entry.label}`}
      >
        <div className="coord-compare-col-head">
          <CastPortrait
            agent={entry.agent}
            model={entry.model || "default"}
            scene="idle"
            className="coord-compare-portrait"
            fallback={
              <span
                className={`station-avatar agent-${agentColorKey(entry.agent)} coord-entry-badge`}
                aria-hidden="true"
              >
                {entry.label}
              </span>
            }
          />
          <div className="coord-compare-col-meta">
            <div className="coord-compare-col-title">{entryTitle}</div>
            {revealIdentity && (
              <div className="coord-compare-col-agent">
                {agentName(entry.agent)}
                {entry.model && entry.model !== "default" ? ` · ${entry.model}` : ""}
              </div>
            )}
            <span className={`coord-compare-status is-${entry.status}`}>
              {t(`runs.status.${entry.status}`)}
            </span>
          </div>
          {entryScore && (
            <span className="coord-score-chip">
              {t("contest.score", { score: entryScore.score })}
            </span>
          )}
        </div>

        {entryScore?.why && (
          <div className="coord-score-why">
            <span className="coord-score-why-label">{t("contest.why")}</span>{" "}
            <span>{entryScore.why}</span>
          </div>
        )}

        {entry.summary && <p className="coord-compare-summary">{entry.summary}</p>}

        <div className="coord-compare-files">
          {!hasChanges ? (
            <span className="coord-compare-no-change">{t("contest.noChange")}</span>
          ) : (
            <>
              <div className="coord-compare-totals">
                <LineCounts additions={entry.additions} deletions={entry.deletions} />
              </div>
              {files.length > 0 && (
                <ul className="coord-compare-file-list">
                  {files.map((file) => (
                    <li key={file.path} className="coord-compare-file">
                      <code className="coord-compare-file-path">{file.path}</code>
                      <LineCounts additions={file.additions} deletions={file.deletions} />
                    </li>
                  ))}
                </ul>
              )}
              {(onShowChanges || runs?.some((r) => r.id === entry.runId)) && (
                <button
                  type="button"
                  className="coord-btn is-small coord-show-changes"
                  onClick={() => onShowChanges?.(entry.runId)}
                >
                  {t("runs.viewChanges")}
                </button>
              )}
            </>
          )}
        </div>

        {canPickEntry(entry) && (
          <div className="coord-compare-pick">
            {!isPicking ? (
              <button
                type="button"
                className="coord-btn is-primary"
                onClick={() => {
                  setPickingRunId(entry.runId);
                  setConfirmingApply(null);
                  setDiscardOthers(true);
                }}
              >
                {t("contest.pick")}
              </button>
            ) : confirmingApply ? (
              <div className="coord-discard-confirm" role="region" aria-label={t("contest.discardOthers")}>
                <p className="coord-discard-list">
                  {t("contest.discardList", { labels: formatList(otherLabels) })}
                </p>
                <div className="coord-discard-confirm-actions">
                  <button
                    type="button"
                    className="coord-btn"
                    onClick={() => setConfirmingApply(null)}
                  >
                    {t("common.cancel")}
                  </button>
                  <button
                    type="button"
                    className="coord-btn is-primary"
                    onClick={() => handleConfirmPick(entry)}
                  >
                    {t("common.confirm")}
                  </button>
                </div>
              </div>
            ) : (
              <div className="coord-apply-panel">
                <div className="coord-apply-buttons">
                  <button
                    type="button"
                    className="coord-btn is-primary"
                    onClick={() => handleSelectApply(entry, "local")}
                  >
                    {t("contest.apply.local")}
                  </button>
                  {!simple && (
                    <button
                      type="button"
                      className="coord-btn"
                      onClick={() => handleSelectApply(entry, "pr")}
                    >
                      {t("contest.apply.pr")}
                    </button>
                  )}
                  <button
                    type="button"
                    className="coord-btn"
                    onClick={() => handleSelectApply(entry, "keep")}
                  >
                    {t("contest.apply.keep")}
                  </button>
                </div>
                {otherLabels.length > 0 && (
                  <label className="coord-discard-check">
                    <input
                      type="checkbox"
                      checked={discardOthers}
                      onChange={(event) => setDiscardOthers(event.target.checked)}
                    />
                    <span>{t("contest.discardOthers")}</span>
                  </label>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    );
  };

  const safeTabIndex = entries.length > 0 ? Math.min(activeTab, entries.length - 1) : 0;

  return (
    <div className={`coord-compare${simple ? " is-simple" : ""}`}>
      <div className="coord-compare-header">
        <span className="coord-compare-title">
          {t("contest.title", { count: contest.entryCount || entries.length })}
        </span>
        <span className={`coord-compare-state is-${contest.state}`}>
          {t(`contest.state.${contest.state}`)}
        </span>
      </div>

      {compare.scores && (
        <p className="coord-compare-advice">{t("contest.advice")}</p>
      )}
      {!compare.scores &&
        Boolean(contest.judgeRunId) &&
        (contest.state === "ready" || contest.state === "decided") && (
          <p className="coord-compare-advice">{t("contest.noScores")}</p>
        )}

      {refusedDiscards.map((act) => {
        const label = entries.find((e) => e.runId === act.runId)?.label ?? act.runId;
        // The hub sends a code, never words: say it as the workspace panel does.
        const reason = workspaceCodeText(act.code) || act.code || act.status;
        return (
          <p key={act.runId} className="coord-discard-refused" role="alert">
            {t("contest.discardRefused", { label, reason })}
          </p>
        );
      })}

      {wide ? (
        <div className="coord-compare-grid">
          {entries.map((entry) => renderEntryCard(entry))}
        </div>
      ) : (
        <div className="coord-compare-stacked">
          <div className="coord-compare-tabs" role="tablist">
            {entries.map((entry, idx) => {
              const selected = idx === safeTabIndex;
              return (
                <button
                  key={entry.runId || entry.label}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  className={`coord-compare-tab${selected ? " is-selected" : ""}`}
                  onClick={() => setActiveTab(idx)}
                >
                  {t("contest.entry", { label: entry.label })}
                </button>
              );
            })}
          </div>
          {entries[safeTabIndex] && renderEntryCard(entries[safeTabIndex])}
        </div>
      )}
    </div>
  );
}
