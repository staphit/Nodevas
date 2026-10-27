import "./explorer.css";
import { Sidebar } from "../components/Sidebar";
import { useApp, usePreference } from "../store";
import AgentsTab from "./AgentsTab";
import ExplorerRail from "./ExplorerRail";
import FilesPanel from "./FilesPanel";
import GitPanel from "./GitPanel";

/** The left sidebar: the rail and the current tab's panel. Nodes is the
 * existing Sidebar, unchanged. */
export default function ExplorerShell() {
  const tab = usePreference("explorerTab");
  return (
    <div className="ex-shell">
      <ExplorerRail tab={tab} onTab={(next) => useApp.getState().updateUIPreference("explorerTab", next)} />
      <div className="ex-panel">
        {tab === "nodes" && <Sidebar />}
        {tab === "files" && <FilesPanel />}
        {tab === "git" && <GitPanel />}
        {tab === "agent" && <AgentsTab />}
      </div>
    </div>
  );
}
