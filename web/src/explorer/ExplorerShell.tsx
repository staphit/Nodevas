import "./explorer.css";
import { Sidebar } from "../components/Sidebar";
import { useNarrowViewport } from "../components/TopbarOverflow";
import { useApp, usePreference } from "../store";
import AgentsTab from "./AgentsTab";
import ExplorerRail from "./ExplorerRail";
import type { ExplorerTab } from "./explorerStore";
import FilesPanel from "./FilesPanel";
import GitPanel from "./GitPanel";

/** Folds the explorer away; the board header's » brings it back, so the
 * keyboard waits there. */
function collapseExplorer() {
  useApp.getState().updateUIPreference("explorerCollapsed", true);
  requestAnimationFrame(() => document.querySelector<HTMLElement>(".board-explorer-toggle")?.focus());
}

/** The left sidebar: the rail and the current tab's panel. Nodes is the
 * existing Sidebar, unchanged. On a phone the panel overlays the board, so
 * the open tab tapped again, or Escape, closes it as well as the rail's «. */
export default function ExplorerShell() {
  const tab = usePreference("explorerTab");
  const narrow = useNarrowViewport();
  const onTab = (next: ExplorerTab) => {
    if (narrow && next === tab) {
      collapseExplorer();
      return;
    }
    useApp.getState().updateUIPreference("explorerTab", next);
  };
  return (
    <div
      className="ex-shell"
      onKeyDown={(event) => {
        if (!narrow || event.key !== "Escape" || event.defaultPrevented) return;
        event.preventDefault();
        collapseExplorer();
      }}
    >
      <ExplorerRail
        tab={tab}
        onTab={onTab}
        // Nodes has its own « in its heading.
        onCollapse={tab === "nodes" ? undefined : collapseExplorer}
      />
      <div className="ex-panel">
        {tab === "nodes" && <Sidebar />}
        {tab === "files" && <FilesPanel />}
        {tab === "git" && <GitPanel />}
        {tab === "agent" && <AgentsTab />}
      </div>
    </div>
  );
}
