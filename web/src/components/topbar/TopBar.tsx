/**
 * The top bar, left to right: the brand; the project menu; Board | Station;
 * then, pushed right, what is happening (offline while the server is out
 * of reach, the last operation, who else is here), search, the runners
 * pill, Settings (a labelled button: one click in, pressed while open), the
 * theme toggle and, on a server with a session, the account avatar.
 *
 * Everything the bar used to carry besides is a menu entry, a Settings row or
 * a palette command now: the explorer toggle (the board's explorer, the
 * palette), the workspace and the layout chip (project menu), help and the
 * tour (Settings › General, the palette), sign-out (account menu), Runners
 * and Usage (the pill, Settings, the palette).
 */

import { useLastOperation } from "../../store";
import { useI18n } from "../../i18n";
import { IconTree } from "../../icons";
import { ModeSwitch } from "../../station/ModeSwitch";
import { OperationStatus } from "../InteractionPrimitives";
import { PresenceAvatars } from "../PresenceAvatars";
import { useNarrowViewport } from "../TopbarOverflow";
import { AccountMenu } from "./AccountMenu";
import { OfflineIndicator } from "./OfflineIndicator";
import { openCommandPalette } from "./actions";
import { ProjectMenu } from "./ProjectMenu";
import { RunnerPill } from "./RunnerPill";

export function TopBar() {
  const { t } = useI18n();
  const lastOperation = useLastOperation();
  // The presence cluster keeps fewer faces on a phone-width bar.
  const narrow = useNarrowViewport();

  return (
    <header className="topbar">
      <span className="brand-mark">
        <IconTree size={17} />
      </span>
      <h1>Nodevas</h1>
      <ProjectMenu />
      <ModeSwitch />
      <span className="topbar-spacer" />
      <OfflineIndicator />
      {lastOperation && (
        <OperationStatus
          status={lastOperation.status}
          message={
            lastOperation.status === "error" || lastOperation.status === "conflict"
              ? lastOperation.message
              : undefined
          }
        />
      )}
      <PresenceAvatars narrow={narrow} />
      <div className="topbar-actions">
        <button
          className="command-trigger"
          type="button"
          onClick={() => openCommandPalette()}
          title={`${t("topbar.searchOrCommand")} (Ctrl/⌘ K)`}
        >
          <span className="command-trigger-text">{t("topbar.searchOrCommand")}</span> <kbd>Ctrl/⌘ K</kbd>
        </button>
        <RunnerPill />
        <AccountMenu />
      </div>
    </header>
  );
}
