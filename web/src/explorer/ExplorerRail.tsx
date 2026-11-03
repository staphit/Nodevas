import type { ReactNode } from "react";
import { useI18n } from "../i18n";
import type { ExplorerTab } from "./explorerStore";

export interface ExplorerRailProps {
  tab: ExplorerTab;
  onTab: (tab: ExplorerTab) => void;
  /** Folds the explorer away; the rail shows a « for it when given. */
  onCollapse?: () => void;
}

const svg = (children: ReactNode) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
    {children}
  </svg>
);

const ICONS: Record<ExplorerTab, ReactNode> = {
  nodes: svg(
    <>
      <rect x="3" y="4" width="7" height="6" rx="1.5" />
      <rect x="14" y="14" width="7" height="6" rx="1.5" />
      <path d="M10 7h4v10" />
    </>,
  ),
  files: svg(<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" />),
  git: svg(
    <>
      <circle cx="6" cy="6" r="2.2" />
      <circle cx="6" cy="18" r="2.2" />
      <circle cx="18" cy="9" r="2.2" />
      <path d="M6 8.2v7.6M18 11.2c0 3-3 4-7.6 5.4" />
    </>,
  ),
  agent: svg(
    <>
      <rect x="5" y="7" width="14" height="11" rx="3" />
      <path d="M12 3v4M9 12h.01M15 12h.01" />
    </>,
  ),
};

export const EXPLORER_TABS: readonly ExplorerTab[] = ["nodes", "files", "git", "agent"];

/** The four tabs: Nodes, Files, Git, Agent. */
export default function ExplorerRail({ tab, onTab, onCollapse }: ExplorerRailProps) {
  const { t } = useI18n();
  return (
    <div className="ex-rail" role="group" aria-label={t("explorer.tabs")}>
      {EXPLORER_TABS.map((value) => (
        <button
          key={value}
          type="button"
          className={value === tab ? "on" : undefined}
          aria-label={t(`explorer.tab.${value}`)}
          aria-pressed={value === tab}
          title={t(`explorer.tab.${value}`)}
          onClick={() => onTab(value)}
        >
          {ICONS[value]}
          <span aria-hidden="true">{t(`explorer.tab.${value}`)}</span>
        </button>
      ))}
      {onCollapse && (
        <button
          type="button"
          className="ex-rail-collapse"
          aria-label={t("board.collapseExplorer")}
          title={t("board.collapseExplorer")}
          aria-expanded
          aria-controls="project-explorer"
          onClick={onCollapse}
        >
          «
        </button>
      )}
    </div>
  );
}
