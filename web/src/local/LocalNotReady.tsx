// What the single-user edition shows wherever an agent would be started or
// set up while this computer cannot run one. One block for the dispatch
// dialog, the global agent form, Station and This computer, so each says the
// same thing and none falls back to a team hub's words about runners.
//
// Starting: the app has only just started and the part that runs agents is
// on its way up. Nothing is wrong and nothing needs pressing: the runner
// list refreshes by itself when it connects. Stopped: it was up and is not
// now (or never came up), so the reader gets what to try, in order.

import { useI18n } from "../i18n";
import { useRunners } from "../runners/runnerStore";

export function LocalNotReady({
  starting,
  onOpenSettings,
}: {
  starting: boolean;
  /** Opens This computer's settings; left out where the block is already there. */
  onOpenSettings?: () => void;
}) {
  const { t } = useI18n();
  if (starting) {
    return (
      <div className="run-empty" role="status" data-testid="local-not-ready">
        <p>{t("local.starting")}</p>
        <p>{t("local.startingNext")}</p>
      </div>
    );
  }
  return (
    <div className="run-empty" role="status" data-testid="local-not-ready">
      <p>{t("local.unavailable")}</p>
      <p>{t("local.unavailableNext")}</p>
      <span>
        <button type="button" className="run-link-button" onClick={() => void useRunners.getState().refresh()}>
          {t("local.recheck")}
        </button>
        {onOpenSettings && (
          <>
            {" "}
            <button type="button" className="run-link-button" onClick={onOpenSettings}>
              {t("offerAsk.fixLocal")}
            </button>
          </>
        )}
      </span>
    </div>
  );
}
