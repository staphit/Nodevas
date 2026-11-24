import { useCallback, useState } from "react";
import { GameDialog } from "../../game/GameActions";
import { useI18n } from "../../i18n";
import { ContestView } from "./ContestView";
import { useCoordStore } from "./coordStore";

/**
 * "Compare and pick" for a contest seen from one of its entries (a node's
 * run view, the story room's pair): opens ContestView, where the person
 * compares the entries and picks one. Without it a node contest could be
 * started but never decided, and its entries' Apply stays locked.
 */
export function ContestDecide({ contestId, simple = false }: { contestId: string; simple?: boolean }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const count = useCoordStore((s) => s.contests[contestId]?.contest.entryCount ?? 0);
  const close = useCallback(() => setOpen(false), []);
  return (
    <>
      <button type="button" className="coord-btn coord-contest-open" onClick={() => setOpen(true)}>
        {t("contest.open")}
      </button>
      {open && (
        <GameDialog title={count > 0 ? t("contest.title", { count }) : t("contest.open")} onClose={close}>
          <ContestView contestId={contestId} simple={simple} />
        </GameDialog>
      )}
    </>
  );
}
