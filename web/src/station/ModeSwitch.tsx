// The top bar's Info | Station switch. Part of the main bundle, so it stays
// small: Station itself loads only when someone opens it.

import { canManageSession, useAgents } from "../agents/agentStore";
import { setProjectMode } from "../game/stageSkin";
import { useI18n } from "../i18n";
import { IconCanvas, IconGear, IconSpark } from "../icons";
import { usePreference } from "../store";

/** Questions and held messages the viewer may answer: Station's badge. */
export function useWaitingCount(): number {
  const asks = useAgents((s) => s.asks);
  const held = useAgents((s) => s.held);
  const viewer = useAgents((s) => s.viewer);
  const pending = asks.filter((ask) => ask.status === "pending" && canManageSession(ask, viewer)).length;
  return pending + held.length;
}

export function ModeSwitch() {
  const { t } = useI18n();
  const mode = usePreference("projectMode");
  const waiting = useWaitingCount();
  return (
    <nav className="mode-switch" aria-label={t("mode.label")}>
      <button
        type="button"
        className={mode === "info" ? "active" : ""}
        aria-pressed={mode === "info"}
        onClick={() => setProjectMode("info")}
        title={t("mode.info.detail")}
        aria-label={t("mode.info")}
      >
        <IconCanvas size={14} />
        <span>{t("mode.info")}</span>
      </button>
      <button
        type="button"
        className={mode === "station" ? "active" : ""}
        aria-pressed={mode === "station"}
        onClick={() => setProjectMode("station")}
        title={t("mode.station.detail")}
        // The names hide on a narrow bar; the label keeps them, and the count.
        aria-label={waiting > 0 ? `${t("mode.station")}, ${t("mode.waiting", { count: waiting })}` : t("mode.station")}
      >
        <IconSpark size={14} />
        <span>{t("mode.station")}</span>
        {waiting > 0 && (
          <span className="mode-switch-badge" aria-hidden="true">
            {waiting}
          </span>
        )}
      </button>
      <button
        type="button"
        className={mode === "settings" ? "active" : ""}
        aria-pressed={mode === "settings"}
        onClick={() => setProjectMode("settings")}
        title={t("mode.settings.detail")}
        aria-label={t("mode.settings")}
      >
        <IconGear size={14} />
        <span>{t("mode.settings")}</span>
      </button>
    </nav>
  );
}
