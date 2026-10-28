/**
 * "Offline: can't reach the server", on the top bar for as long as it is
 * true. The error pill only lasts as long as the failed operation it reports;
 * this stays up from the first request or socket that could not reach the
 * server until one gets through again (see api/connectivity.ts).
 */

import { useConnectivity } from "../../api/connectivity";
import { useI18n } from "../../i18n";

export function OfflineIndicator() {
  const { t } = useI18n();
  const offline = useConnectivity((s) => s.offline);
  if (!offline) return null;
  return (
    <span className="topbar-offline" role="status" title={t("topbar.offlineTitle")}>
      <span className="topbar-offline-dot" aria-hidden />
      <span className="topbar-offline-text">{t("topbar.offline")}</span>
    </span>
  );
}
