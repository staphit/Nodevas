/**
 * Status legend [B-05/B-06].
 *
 * Read-only on purpose: workflow definitions have exactly one editor, Project
 * Settings. The legend explains the shapes, it does not create them.
 */

import { useApp } from "../../store";
import { STATUS_THEME, StatusShape, statusTheme } from "../../statusTheme";
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
  const customStatuses = useApp((state) => state.graph?.ui?.customStatuses) ?? [];
  return (
    <details className="sidebar-legend">
      <summary>狀態圖例</summary>
      <ul className="legend">
        {LEGEND_STATUSES.map((status) => (
          <li key={status}>
            <StatusShape status={status} />
            <span>{STATUS_THEME[status].label}</span>
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
        自訂實際狀態在「專案設定 → 實際狀態」（頂端工具列的齒輪）管理。
      </p>
      <button
        type="button"
        className="legend-settings-link"
        onClick={() =>
          window.dispatchEvent(new Event("nodevas-project-settings"))
        }
      >
        開啟專案設定
      </button>
    </details>
  );
}
