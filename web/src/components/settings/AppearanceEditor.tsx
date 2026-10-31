import { useApp, usePreference } from "../../store";
import { LANGUAGE_NATIVE_NAMES, LANGUAGE_OPTIONS, useI18n } from "../../i18n";
import { EDITOR_FONT_SIZE, EDITOR_MODES, TEXT_SCALE } from "../../preferences/schema";
import { confirmAction } from "../ConfirmDialog";
import { openShortcutsHelp, replayTour } from "../topbar/actions";
import type { SettingsNotify } from "./notify";

/** Local preferences only — nothing here reaches the project files. The
 * theme, the language, the tour and help; the rest of the local preferences
 * are LayoutEditor's, a row of their own in Settings. */
export function AppearanceEditor() {
  const { language, setLanguage, t } = useI18n();
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const textScale = usePreference("textScale");
  const percent = `${Math.round(textScale * 100)}%`;

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
            <option key={option} value={option} lang={option}>
              {LANGUAGE_NATIVE_NAMES[option]}
            </option>
          ))}
        </select>
      </div>
      <div className="settings-row">
        <span>{t("appearance.textSize")}</span>
        <input
          type="range"
          className="settings-text-size"
          min={TEXT_SCALE.min}
          max={TEXT_SCALE.max}
          step={0.05}
          value={textScale}
          aria-label={t("appearance.textSize")}
          aria-valuetext={percent}
          onChange={(event) => updateUIPreference("textScale", Number(event.target.value))}
        />
        <output className="settings-text-size-value">{percent}</output>
        <button
          type="button"
          disabled={textScale === TEXT_SCALE.fallback}
          onClick={() => updateUIPreference("textScale", TEXT_SCALE.fallback)}
        >
          {t("appearance.resetTextSize")}
        </button>
      </div>
      <div className="settings-row">
        <span>{t("appearance.tour")}</span>
        {/* The tour points at the board, so it plays there. */}
        <button type="button" onClick={replayTour}>
          {t("appearance.replayTour")}
        </button>
      </div>
      <div className="settings-row">
        <span>{t("appearance.help")}</span>
        <button type="button" onClick={openShortcutsHelp}>
          {t("topbar.helpShortcuts")}
        </button>
      </div>
    </section>
  );
}

/** The editor's defaults and the layout resets: local preferences too,
 * less often changed. */
export function LayoutEditor({ notify }: { notify: SettingsNotify }) {
  const { t } = useI18n();
  const resetUIPreferences = useApp((s) => s.resetUIPreferences);
  const updateUIPreference = useApp((s) => s.updateUIPreference);
  const editorMode = usePreference("editorMode");
  const editorFontSize = usePreference("editorFontSize");
  const editorOutline = usePreference("editorOutline");
  const fontSizes = Array.from(
    { length: EDITOR_FONT_SIZE.max - EDITOR_FONT_SIZE.min + 1 },
    (_, index) => EDITOR_FONT_SIZE.min + index,
  );

  return (
    <section className="settings-section">
      {/* The same preferences the drawer's toolbar changes: a default here is
          whatever the editor was last left at. */}
      <p className="settings-hint">{t("appearance.editorHint")}</p>
      <div className="settings-row">
        <span>{t("editor.mode")}</span>
        <select
          value={editorMode}
          aria-label={t("editor.mode")}
          onChange={(event) =>
            updateUIPreference("editorMode", event.target.value as (typeof EDITOR_MODES)[number])
          }
        >
          {EDITOR_MODES.map((mode) => (
            <option key={mode} value={mode}>
              {t(`editor.${mode}Label`)}
            </option>
          ))}
        </select>
      </div>
      <div className="settings-row">
        <span>{t("editor.fontSize")}</span>
        <select
          value={editorFontSize}
          aria-label={t("editor.fontSize")}
          onChange={(event) => updateUIPreference("editorFontSize", Number(event.target.value))}
        >
          {fontSizes.map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </select>
        <label>
          <input
            type="checkbox"
            checked={editorOutline}
            onChange={(event) => updateUIPreference("editorOutline", event.target.checked)}
          />{" "}
          {t("appearance.editorOutline")}
        </label>
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
            // Every preference, except where the person is: resetting from
            // the Settings view should not throw them out of it.
            const { projectMode, settingsSection } = useApp.getState().preferences;
            resetUIPreferences();
            updateUIPreference("projectMode", projectMode);
            updateUIPreference("settingsSection", settingsSection);
            notify.onNotice(t("appearance.resetAllNotice"));
          }}
        >
          {t("appearance.resetAll")}
        </button>
      </div>
    </section>
  );
}
