/**
 * Status legend [B-05/B-06].
 *
 * Read-only on purpose: workflow definitions have exactly one editor, Project
 * Settings. The legend explains the shapes, it does not create them.
 */

import { useApp } from "../../store";
import { useI18n } from "../../i18n";
import { StatusShape, statusTheme } from "../../statusTheme";
import type { BuiltinStatus } from "../../types";

const LEGEND_STATUSES: BuiltinStatus[] = [
  "locked",
  "ready",
  "started",
  "in_progress",
  "done",
  "skipped",
  "failed",
];

export function StatusLegend() {
  const { t } = useI18n();
  const customStatuses = useApp((state) => state.graph?.ui?.customStatuses) ?? [];
  return (
    <details className="sidebar-legend">
      <summary>{t("sidebar.statusLegend")}</summary>
      <ul className="legend">
        {LEGEND_STATUSES.map((status) => (
          <li key={status}>
            <StatusShape status={status} />
            <span>{t(`status.${status}`)}</span>
          </li>
        ))}
        {customStatuses.map((definition) => (
          <li key={definition.id}>
            <StatusShape status={definition.id} definitions={customStatuses} />
            <span>{statusTheme(definition.id, customStatuses).label}</span>
          </li>
        ))}
      </ul>
      <p className="legend-hint">
        {t("sidebar.statusHint")}
      </p>
      <button
        type="button"
        className="legend-settings-link"
        onClick={() =>
          window.dispatchEvent(new Event("nodevas-project-settings"))
        }
      >
        {t("sidebar.openProjectSettings")}
      </button>
    </details>
  );
}
