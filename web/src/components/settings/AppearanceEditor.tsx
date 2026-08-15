import { useApp } from "../../store";
import { LANGUAGE_OPTIONS, useI18n } from "../../i18n";
import { confirmAction } from "../ConfirmDialog";
import type { SettingsNotify } from "./notify";

/** Local preferences only — nothing here reaches the project files. */
export function AppearanceEditor({ notify }: { notify: SettingsNotify }) {
  const { language, setLanguage, t } = useI18n();
  const resetUIPreferences = useApp((s) => s.resetUIPreferences);
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);

  return (
    <section className="settings-section">
      <p className="settings-hint">
        {t("appearance.localOnly")}
      </p>
      <div className="settings-row">
        <span>{t("appearance.theme")}</span>
        <button type="button" onClick={toggleTheme}>
          {theme === "dark" ? t("appearance.switchLight") : t("appearance.switchDark")}
        </button>
      </div>
      <div className="settings-row">
        <span>{t("appearance.language")}</span>
        <select
          value={language}
          aria-label={t("language.label")}
          onChange={(event) => setLanguage(event.target.value as typeof language)}
        >
          {LANGUAGE_OPTIONS.map((option) => (
            <option key={option} value={option}>
              {t(`language.option.${option}`)}
            </option>
          ))}
        </select>
      </div>
      <div className="settings-row">
        <span>{t("appearance.layout")}</span>
        <button
          type="button"
          onClick={() => {
            resetUIPreferences("layout");
            notify.onNotice(t("appearance.layoutReset"));
          }}
        >
          {t("appearance.resetLayout")}
        </button>
        <button
          type="button"
          onClick={() => {
            resetUIPreferences("timeline");
            notify.onNotice(t("appearance.timelineReset"));
          }}
        >
          {t("appearance.resetTimeline")}
        </button>
      </div>
      <div className="settings-row">
        <span>{t("appearance.all")}</span>
        <button
          type="button"
          className="danger"
          onClick={async () => {
            const confirmed = await confirmAction({
              title: t("appearance.resetAllTitle"),
              description: t("appearance.resetAllDescription"),
              confirmLabel: t("appearance.reset"),
              tone: "danger",
            });
            if (!confirmed) return;
            resetUIPreferences();
            notify.onNotice(t("appearance.resetAllNotice"));
          }}
        >
          {t("appearance.resetAll")}
        </button>
      </div>
    </section>
  );
}
