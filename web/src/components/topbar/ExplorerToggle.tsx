/**
 * The project explorer's show / hide button, as the top bar drew it. The bar
 * no longer does; whatever holds the explorer renders this (it controls
 * #project-explorer), and the command palette keeps its own entry.
 */

import { IconTree } from "../../icons";
import { useI18n } from "../../i18n";
import { usePreference } from "../../store";
import { toggleExplorer } from "./actions";

export function ExplorerToggle({ className = "icon-btn explorer-toggle" }: { className?: string }) {
  const { t } = useI18n();
  const collapsed = usePreference("explorerCollapsed");
  const label = collapsed ? t("topbar.explorer.expand") : t("topbar.explorer.collapse");
  return (
    <button
      type="button"
      className={className}
      onClick={toggleExplorer}
      title={label}
      aria-label={label}
      aria-expanded={!collapsed}
      aria-controls="project-explorer"
    >
      <IconTree size={15} />
      <span aria-hidden>{collapsed ? "»" : "«"}</span>
    </button>
  );
}
