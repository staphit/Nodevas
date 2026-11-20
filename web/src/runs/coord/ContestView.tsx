import { useEffect, useState } from "react";
import type {
  Contest,
  ContestCompare as ContestCompareData,
  PickActionResult,
  PickContestRequest,
  Run,
} from "../../api";
import { useI18n } from "../../i18n";
import { RunDiffDialog } from "../DiffViewer";
import { ContestCompare } from "./ContestCompare";
import { useCoordStore } from "./coordStore";

export interface ContestViewProps {
  contestId: string;
  contest?: Contest;
  compare?: ContestCompareData;
  runs?: readonly Run[] | null;
  pickActions?: readonly PickActionResult[] | null;
  simple?: boolean;
  onShowChanges?: (runId: string) => void;
  onClose?: () => void;
}

export function ContestView({
  contestId,
  contest: givenContest,
  compare: givenCompare,
  runs: givenRuns,
  pickActions: givenActions,
  simple = false,
  onShowChanges,
  onClose,
}: ContestViewProps) {
  const { t } = useI18n();
  const detail = useCoordStore((s) => s.contests[contestId]);
  const loading = useCoordStore((s) => Boolean(s.contestLoading[contestId]));
  const error = useCoordStore((s) => s.contestErrors[contestId]);
  const storedActions = useCoordStore((s) => s.pickActions[contestId]);
  const picking = useCoordStore((s) => Boolean(s.picking[contestId]));
  const [diffRunId, setDiffRunId] = useState<string | null>(null);

  useEffect(() => {
    if (!givenContest || !givenCompare) {
      void useCoordStore.getState().loadContest(contestId);
    }
  }, [contestId, givenContest, givenCompare]);

  const contest = givenContest ?? detail?.contest;
  const compare = givenCompare ?? detail?.compare;
  const runs = givenRuns ?? detail?.runs ?? null;
  const pickActions = givenActions ?? storedActions ?? null;

  if (!contest || !compare) {
    return (
      <div className="coord-contest-view">
        {loading ? (
          <p className="coord-contest-status">{t("common.loading")}</p>
        ) : error ? (
          <p className="coord-contest-error" role="alert">
            {error}
          </p>
        ) : null}
      </div>
    );
  }

  const diffRun = diffRunId ? runs?.find((r) => r.id === diffRunId) ?? null : null;

  const handlePick = async (req: PickContestRequest) => {
    await useCoordStore.getState().pickContest(contest.id, req);
  };

  const handleShowChanges = (runId: string) => {
    if (onShowChanges) {
      onShowChanges(runId);
      return;
    }
    setDiffRunId(runId);
  };

  return (
    <div className="coord-contest-view">
      {error && (
        <p className="coord-contest-error" role="alert">
          {error}
        </p>
      )}
      <ContestCompare
        contest={contest}
        compare={compare}
        runs={runs}
        pickActions={pickActions}
        simple={simple}
        disabled={picking}
        onPick={handlePick}
        onShowChanges={handleShowChanges}
      />
      {onClose && (
        <div className="coord-contest-view-footer">
          <button type="button" className="coord-btn" onClick={onClose}>
            {t("common.close")}
          </button>
        </div>
      )}
      {diffRun && <RunDiffDialog run={diffRun} onClose={() => setDiffRunId(null)} />}
    </div>
  );
}
