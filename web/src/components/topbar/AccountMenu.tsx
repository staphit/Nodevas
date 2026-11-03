/**
 * The account menu at the very end of the bar: the signed-in viewer's
 * initial, opening onto their account and sign-out. Only a server with a
 * session draws it; a standalone (loopback) app has no account, so nothing.
 * Settings, the theme, the tour and help are not here: Settings and the
 * theme are buttons on the bar, the tour and help rows of Settings › General
 * (and palette commands).
 */

import { IconShield, IconSignOut } from "../../icons";
import { useI18n } from "../../i18n";
import { openSettings } from "../../settings/openSettings";
import { useSignOut, useViewer } from "../SignIn";
import { TopbarMenu, type TopbarMenuEntry } from "./TopbarMenu";

export function AccountMenu() {
  const { t } = useI18n();
  const viewer = useViewer();
  const signOut = useSignOut();
  if (!viewer) return null;
  const who = viewer.name || viewer.role;

  const entries: TopbarMenuEntry[] = [
    {
      id: "account",
      label: t("topbar.yourAccount"),
      icon: <IconShield size={15} />,
      onSelect: () => openSettings("account"),
    },
    {
      id: "sign-out",
      label: t("topbar.signOut", { name: who }),
      icon: <IconSignOut size={15} />,
      separatorBefore: true,
      onSelect: signOut,
    },
  ];

  return (
    <TopbarMenu
      className="topbar-account"
      triggerClassName="topbar-avatar"
      align="end"
      label={t("topbar.account")}
      title={who ? t("topbar.accountNamed", { name: who }) : t("topbar.account")}
      trigger={<span aria-hidden>{(who || "?").slice(0, 1).toUpperCase()}</span>}
      entries={entries}
    />
  );
}
