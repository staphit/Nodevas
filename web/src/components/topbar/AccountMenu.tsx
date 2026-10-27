/**
 * The account menu at the end of the bar: what the help, theme and sign-out
 * icons and the Settings mode button used to be. Settings is entered from
 * here (and from the command palette); sign-out is offered only where there
 * is a session to end.
 */

import { setProjectMode } from "../../game/stageSkin";
import { IconCompass, IconGear, IconHelp, IconMoon, IconSignOut, IconSun } from "../../icons";
import { useI18n } from "../../i18n";
import { useApp } from "../../store";
import { useSignOut, useViewer } from "../SignIn";
import { openShortcutsHelp, replayTour } from "./actions";
import { TopbarMenu, type TopbarMenuEntry } from "./TopbarMenu";

export function AccountMenu() {
  const { t } = useI18n();
  const theme = useApp((s) => s.theme);
  const toggleTheme = useApp((s) => s.toggleTheme);
  const viewer = useViewer();
  const signOut = useSignOut();
  const who = viewer ? viewer.name || viewer.role : "";

  const entries: TopbarMenuEntry[] = [
    {
      id: "settings",
      label: t("topbar.settings"),
      icon: <IconGear size={15} />,
      onSelect: () => setProjectMode("settings"),
    },
    {
      id: "theme",
      label: theme === "dark" ? t("topbar.lightMode") : t("topbar.darkMode"),
      icon: theme === "dark" ? <IconSun size={15} /> : <IconMoon size={15} />,
      onSelect: toggleTheme,
    },
    {
      id: "tour",
      label: t("topbar.replayTour"),
      icon: <IconCompass size={15} />,
      onSelect: replayTour,
    },
    {
      id: "help",
      label: t("topbar.helpShortcuts"),
      icon: <IconHelp size={15} />,
      onSelect: openShortcutsHelp,
    },
  ];
  if (viewer) {
    entries.push({
      id: "sign-out",
      label: t("topbar.signOut", { name: who }),
      icon: <IconSignOut size={15} />,
      separatorBefore: true,
      onSelect: signOut,
    });
  }

  return (
    <TopbarMenu
      className="topbar-account"
      triggerClassName="topbar-avatar"
      align="end"
      label={t("topbar.account")}
      title={who ? t("topbar.accountNamed", { name: who }) : t("topbar.account")}
      // The tour's "replay any time" step points here.
      dataTour="tour-replay"
      trigger={
        who ? (
          <span aria-hidden>{who.slice(0, 1).toUpperCase()}</span>
        ) : (
          <IconGear size={16} />
        )
      }
      entries={entries}
    />
  );
}
