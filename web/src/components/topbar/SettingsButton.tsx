/**
 * The end of the bar: a labelled Settings button and the theme toggle.
 *
 * Settings is one click away and says so in words: the button opens the
 * Settings view, shows pressed while it is open, and a second press goes
 * back to where the viewer was (Board or Station). On a phone the word
 * yields and the gear stays; its name does not change.
 */

import { useEffect, useRef } from "react";

import { setProjectMode, type ProjectMode } from "../../game/stageSkin";
import { IconMoon, IconSun } from "../../icons";
import { useI18n } from "../../i18n";
import { useApp, usePreference } from "../../store";

/** A toothed cog. The shared IconGear is a dot with rays and reads as the
 * theme toggle's sun beside it; this one has to say "settings" on its own
 * when the word hides on a phone. */
function Cog({ size = 15 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.75}
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19.11 9.96 21.42 10.17 21.42 13.83 19.11 14.04 18.47 15.59 19.96 17.37 17.37 19.96 15.59 18.47 14.04 19.11 13.83 21.42 10.17 21.42 9.96 19.11 8.41 18.47 6.63 19.96 4.04 17.37 5.53 15.59 4.89 14.04 2.58 13.83 2.58 10.17 4.89 9.96 5.53 8.41 4.04 6.63 6.63 4.04 8.41 5.53 9.96 4.89 10.17 2.58 13.83 2.58 14.04 4.89 15.59 5.53 17.37 4.04 19.96 6.63 18.47 8.41Z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  );
}

export function SettingsButton() {
  const { t } = useI18n();
  const mode = usePreference("projectMode");
  const open = mode === "settings";
  // Where a second press goes back to: the last mode that was not Settings,
  // however Settings was entered (here, the palette, a banner).
  const back = useRef<Exclude<ProjectMode, "settings">>("info");
  useEffect(() => {
    if (mode !== "settings") back.current = mode;
  }, [mode]);

  return (
    <button
      type="button"
      className={`topbar-settings${open ? " active" : ""}`}
      aria-pressed={open}
      // The word hides on a phone; the name stays.
      aria-label={t("topbar.settings")}
      title={open ? t("topbar.settingsClose") : t("topbar.settingsTitle")}
      // The tour's "replay any time" step points here: Replay tour is in
      // Settings › General.
      data-tour="tour-replay"
      onClick={() => setProjectMode(open ? back.current : "settings")}
    >
      <Cog />
      <span className="topbar-settings-text">{t("topbar.settings")}</span>
    </button>
  );
}

export function ThemeButton() {
  const { t } = useI18n();
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);
  const label = theme === "dark" ? t("topbar.lightMode") : t("topbar.darkMode");
  return (
    <button type="button" className="topbar-theme" aria-label={label} title={label} onClick={toggleTheme}>
      {theme === "dark" ? <IconSun size={15} /> : <IconMoon size={15} />}
    </button>
  );
}
